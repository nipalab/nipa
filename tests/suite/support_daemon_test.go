package suite_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/nipalab/nipa/internal/client/daemon"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

type daemonEndpoint struct {
	PID     int    `json:"pid"`
	Port    int    `json:"port"`
	Token   string `json:"token"`
	Version string `json:"version"`
}

type daemonProc struct {
	cmd      *exec.Cmd
	endpoint string
	logPath  string
	info     daemonEndpoint
	done     chan struct{}

	conn   *grpc.ClientConn
	client daemonpb.NipaDaemonClient
	ctx    context.Context
}

func startDaemon() *daemonProc {
	GinkgoHelper()

	dir := GinkgoT().TempDir()
	endpoint := filepath.Join(dir, "daemon.json")
	logPath := filepath.Join(dir, "daemon.log")
	logFile, err := os.Create(logPath)
	Expect(err).NotTo(HaveOccurred())

	cmd := exec.Command(cli.binary, "serve", "--port", "0", "--endpoint", endpoint)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NIPA_TOKEN_FILE="+cli.tokenFile)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	Expect(cmd.Start()).To(Succeed())

	d := &daemonProc{cmd: cmd, endpoint: endpoint, logPath: logPath, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(d.done)
	}()
	DeferCleanup(d.stop)

	Eventually(func() error {
		data, err := os.ReadFile(endpoint)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, &d.info)
	}, "10s", "100ms").Should(Succeed(), func() string {
		log, _ := os.ReadFile(logPath)
		return "daemon log: " + string(log)
	})

	conn, err := grpc.NewClient(
		fmt.Sprintf("127.0.0.1:%d", d.info.Port),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	Expect(err).NotTo(HaveOccurred())
	d.conn = conn
	d.client = daemonpb.NewNipaDaemonClient(conn)
	d.ctx = metadata.AppendToOutgoingContext(context.Background(), daemon.TokenHeader, d.info.Token)
	return d
}

func (d *daemonProc) stop() {
	if d.conn != nil {
		_ = d.conn.Close()
	}
	if d.cmd.Process != nil {
		_ = d.cmd.Process.Kill()
	}
	select {
	case <-d.done:
	case <-time.After(5 * time.Second):
	}
}

func (d *daemonProc) watch(root string) *daemonpb.RepoInfo {
	GinkgoHelper()
	resp, err := d.client.WatchRepo(d.ctx, &daemonpb.WatchRepoRequest{Root: root})
	Expect(err).NotTo(HaveOccurred())
	return resp.GetRepo()
}

func (d *daemonProc) status(root string) *daemonpb.StatusResponse {
	GinkgoHelper()
	resp, err := d.client.Status(d.ctx, &daemonpb.StatusRequest{Root: root})
	Expect(err).NotTo(HaveOccurred())
	return resp
}

func (d *daemonProc) stage(root string, add, unstage []string) *daemonpb.StatusResponse {
	GinkgoHelper()
	resp, err := d.client.Stage(d.ctx, &daemonpb.StageRequest{Root: root, Add: add, Unstage: unstage})
	Expect(err).NotTo(HaveOccurred())
	return resp
}

type daemonOpStream interface {
	Recv() (*daemonpb.OpEvent, error)
}

func collectOpEvents(stream daemonOpStream) []*daemonpb.OpEvent {
	GinkgoHelper()
	var events []*daemonpb.OpEvent
	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return events
		}
		Expect(err).NotTo(HaveOccurred())
		events = append(events, event)
	}
}

func opResult(events []*daemonpb.OpEvent) *daemonpb.OpResult {
	for i := len(events) - 1; i >= 0; i-- {
		if result := events[i].GetResult(); result != nil {
			return result
		}
	}
	return nil
}

func opFailure(events []*daemonpb.OpEvent) *daemonpb.OpFailure {
	for i := len(events) - 1; i >= 0; i-- {
		if failure := events[i].GetFailure(); failure != nil {
			return failure
		}
	}
	return nil
}

func collectDiff(stream daemonpb.NipaDaemon_DiffClient) (string, *daemonpb.OpFailure) {
	GinkgoHelper()
	var out bytes.Buffer
	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return out.String(), nil
		}
		Expect(err).NotTo(HaveOccurred())
		if failure := event.GetFailure(); failure != nil {
			return out.String(), failure
		}
		out.Write(event.GetData())
	}
}

func contextWithoutToken() context.Context {
	return context.Background()
}

func metadataAppend(_ context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), daemon.TokenHeader, token)
}
