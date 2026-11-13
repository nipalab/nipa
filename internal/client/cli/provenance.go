package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nipalab/nipa/internal/client/assetscan"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/output"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/provenance"
)

func (c *Cli) setupProvenanceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provenance",
		Short: "Record and report where generated assets came from",
		Long:  "Manage the versioned .nipaprovenance.yml at the repository root: one record per asset with the generating tool, model, seed, license, a SHA-256 of the prompt (never the prompt itself) and the content hash at record time. Records are declarations, not proof; C2PA signatures are not verified.",
	}
	cmd.AddCommand(c.setupProvenanceInitCmd(), c.setupProvenanceShowCmd(), c.setupProvenanceSetCmd(), c.setupProvenanceReportCmd())
	return cmd
}

func stageProvenance(cmd *cobra.Command, wc *usecase.WorkingCopy) error {
	return wc.Add(cmd.Context(), []string{provenance.FileName}, usecase.AddOptions{Force: true, NoProvenance: true})
}

func (c *Cli) setupProvenanceInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "init",
		Short:         "Create an empty .nipaprovenance.yml and stage it",
		Long:          "Create an empty .nipaprovenance.yml at the repository root and stage it. Its presence opts the repository in: nipa add then records the provenance of new generated images automatically.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			if _, err := os.Stat(filepath.Join(root, provenance.FileName)); err == nil {
				return domain.NewUserError(provenance.FileName + " already exists")
			}
			wc, cleanup, err := openWorkingCopy()
			if err != nil {
				return err
			}
			defer cleanup()
			if err := usecase.SaveProvenance(root, provenance.New()); err != nil {
				return err
			}
			if err := stageProvenance(cmd, wc); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "created %s\n", provenance.FileName)
			return nil
		},
	}
}

type provenanceShow struct {
	Path   string             `json:"path"`
	Record *provenance.Record `json:"record"`
	Status string             `json:"status"`
}

func repoPath(arg string) (string, error) {
	n, err := provenance.NormalizePath(arg)
	if err != nil {
		return "", domain.NewUserError(err.Error())
	}
	return n, nil
}

func (c *Cli) setupProvenanceShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "show <path>",
		Short:         "Show the provenance record of a file",
		Long:          "Print the provenance record of a file and its status against the working file: recorded (unchanged since recorded), modified (changed and not marked human_edited), edited (changed and marked human_edited) or missing (no record).",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			p, err := repoPath(args[0])
			if err != nil {
				return err
			}
			f, err := usecase.LoadProvenance(root)
			if err != nil {
				return err
			}
			if f == nil {
				return domain.NewUserError("no " + provenance.FileName + "; run nipa provenance init")
			}
			show := provenanceShow{Path: p, Record: f.Get(p)}
			hash, err := usecase.HashWorkingFile(root, p)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			show.Status = provenance.Status(show.Record, hash)
			out := cmd.OutOrStdout()
			if jsonRequested(cmd) {
				return output.WriteJSON(out, show)
			}
			writeProvenanceShow(out, show)
			return nil
		},
	}
	addJSONFlag(cmd)
	return cmd
}

func writeProvenanceShow(out io.Writer, s provenanceShow) {
	_, _ = fmt.Fprintf(out, "%s: %s\n", s.Path, s.Status)
	r := s.Record
	if r == nil {
		return
	}
	field := func(name, value string) {
		if value != "" {
			_, _ = fmt.Fprintf(out, "  %-15s %s\n", name+":", value)
		}
	}
	field("tool", r.Tool)
	field("model", r.Model)
	if r.Seed != nil {
		field("seed", fmt.Sprint(*r.Seed))
	}
	field("prompt_sha256", r.PromptSHA256)
	field("license", r.License)
	field("human_edited", fmt.Sprint(r.HumanEdited))
	field("digital_source", r.DigitalSourceType)
	field("source", r.Source)
	if r.C2PAVerified != nil {
		field("c2pa_verified", fmt.Sprint(*r.C2PAVerified))
	}
	if !r.RecordedAt.IsZero() {
		field("recorded_at", r.RecordedAt.UTC().Format("2006-01-02T15:04:05Z"))
	}
	field("content_hash", r.ContentHash)
	field("note", r.Note)
}

func (c *Cli) setupProvenanceSetCmd() *cobra.Command {
	var (
		tool, model, promptFile, license, note string
		seed                                   uint64
		humanEdited                            bool
	)
	cmd := &cobra.Command{
		Use:           "set <path>",
		Short:         "Record or update the provenance of a file by hand",
		Long:          "Create or update the provenance record of a file. Only the given flags change; the record's source becomes manual and its content hash and time are refreshed from the working file. --prompt-file is hashed (SHA-256 of its trimmed text) and never stored. Creates .nipaprovenance.yml when missing (which opts the repository in) and stages it.",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := localrepo.FindRepoRoot(); err != nil {
				return err
			}
			p, err := repoPath(args[0])
			if err != nil {
				return err
			}
			flags := cmd.Flags()
			in := usecase.ProvenanceInput{}
			if flags.Changed("tool") {
				in.Tool = &tool
			}
			if flags.Changed("model") {
				in.Model = &model
			}
			if flags.Changed("seed") {
				in.Seed = &seed
			}
			if flags.Changed("license") {
				in.License = &license
			}
			if flags.Changed("human-edited") {
				in.HumanEdited = &humanEdited
			}
			if flags.Changed("note") {
				in.Note = &note
			}
			if flags.Changed("prompt-file") {
				data, err := os.ReadFile(promptFile)
				if err != nil {
					return domain.NewUserError(fmt.Sprintf("read prompt file: %v", err))
				}
				hash := provenance.PromptHash(strings.TrimSpace(string(data)))
				in.PromptSHA256 = &hash
			}
			wc, cleanup, err := openWorkingCopy()
			if err != nil {
				return err
			}
			defer cleanup()
			if _, err := wc.SetProvenance(cmd.Context(), p, in); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "recorded provenance for %s\n", p)
			return nil
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&tool, "tool", "", "Generating tool (e.g. ComfyUI, Firefly, ElevenLabs)")
	flags.StringVar(&model, "model", "", "Model or checkpoint name")
	flags.Uint64Var(&seed, "seed", 0, "Generation seed")
	flags.StringVar(&promptFile, "prompt-file", "", "File holding the prompt; only its SHA-256 is stored")
	flags.StringVar(&license, "license", "", "Model or output license")
	flags.BoolVar(&humanEdited, "human-edited", false, "Mark the asset as edited by a human after generation")
	flags.StringVar(&note, "note", "", "Free-form note")
	return cmd
}

func (c *Cli) setupProvenanceReportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "report [--since <rev>] [<path>...]",
		Short:         "Report the provenance of the project's assets",
		Long:          "List files with their provenance record or missing, and whether they changed since recorded, with totals by tool and model for disclosure forms. With --since, the files added or modified since that revision (branch, tag, HEAD or commit id, like nipa diff) up to the working copy; otherwise every recorded file plus every tracked file a require_provenance rule in .nipaassets.yml covers. Records whose file is gone are listed as orphaned.",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			since, _ := cmd.Flags().GetString("since")
			wc, cleanup, err := openWorkingCopy()
			if err != nil {
				return err
			}
			defer cleanup()
			d := c.useCase.Diff()
			if since != "" && d == nil {
				return domain.NewUserError("diff is not available")
			}
			report, err := assetscan.ScanProvenance(cmd.Context(), d, wc, root, assetscan.ProvenanceRequest{Since: since, Paths: args})
			if err != nil {
				return err
			}
			if d != nil {
				d.PruneCache()
			}
			out := cmd.OutOrStdout()
			if jsonRequested(cmd) {
				return output.WriteJSON(out, report)
			}
			writeProvenanceReport(out, report)
			return nil
		},
	}
	cmd.Flags().String("since", "", "Only files added or modified since this revision")
	addJSONFlag(cmd)
	return cmd
}

func writeProvenanceReport(out io.Writer, r *assetscan.ProvenanceReport) {
	if !r.Config {
		_, _ = fmt.Fprintf(out, "no %s; every file is missing a record\n", provenance.FileName)
	}
	for _, f := range r.Files {
		line := fmt.Sprintf("%-9s %s", f.Status, f.Path)
		if f.Record != nil {
			parts := []string{}
			for _, v := range []string{f.Record.Tool, f.Record.Model, f.Record.License} {
				if v != "" {
					parts = append(parts, v)
				}
			}
			if len(parts) > 0 {
				line += "  (" + strings.Join(parts, ", ") + ")"
			}
		}
		_, _ = fmt.Fprintln(out, line)
	}
	for _, p := range r.Orphaned {
		_, _ = fmt.Fprintf(out, "%-9s %s\n", "orphaned", p)
	}
	t := r.Totals
	_, _ = fmt.Fprintf(out, "%d file(s): %d recorded, %d modified, %d edited, %d missing\n", t.Files, t.Recorded, t.Modified, t.Edited, t.Missing)
	writeCounts(out, "tools", t.ByTool)
	writeCounts(out, "models", t.ByModel)
}

func writeCounts(out io.Writer, title string, counts map[string]int) {
	if len(counts) == 0 {
		return
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	_, _ = fmt.Fprintf(out, "%s:\n", title)
	for _, k := range keys {
		_, _ = fmt.Fprintf(out, "  %s: %d\n", k, counts[k])
	}
}
