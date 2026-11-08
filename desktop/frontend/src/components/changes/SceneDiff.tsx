import { useMemo, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, FileText, ListTree, Loader2 } from "lucide-react";
import { desktop, type FileDiff, type SceneChange, type SceneCompare, type SceneObject } from "@/api/desktop";
import { imageRefs, viewerKey } from "@/components/changes/ImageDiff";
import { MetaDiff } from "@/components/changes/Notices";
import { TextDiff, type DiffLayout, type LineComments } from "@/components/changes/TextDiff";
import { Group, Notice, ToolButton } from "@/components/changes/Toolbar";
import { cn } from "@/lib/utils";

type Mode = "structure" | "text";

export function SceneDiff({ root, diff, layout, comments }: { root: string; diff: FileDiff; layout: DiffLayout; comments?: LineComments }) {
  const hasText = (diff.hunks ?? []).length > 0;
  const [mode, setMode] = useState<Mode>(comments && hasText ? "text" : "structure");
  const { old, next } = imageRefs(diff);
  const query = useQuery({
    queryKey: viewerKey(root, diff, "scene"),
    queryFn: () => desktop.sceneDiff(root, old, next),
    staleTime: 30_000,
    enabled: mode === "structure",
  });
  const switcher = hasText && (
    <Group label="Scene view">
      <ToolButton active={mode === "structure"} label="Structure" onClick={() => setMode("structure")}>
        <ListTree className="size-3.5" />
      </ToolButton>
      <ToolButton active={mode === "text"} label="Text" onClick={() => setMode("text")}>
        <FileText className="size-3.5" />
      </ToolButton>
    </Group>
  );
  if (mode === "text") {
    return (
      <div className="flex h-full min-h-0 flex-col">
        <div className="flex shrink-0 items-center border-b border-border px-3 py-1.5">{switcher}</div>
        <div className="min-h-0 flex-1">
          <TextDiff diff={diff} layout={layout} comments={comments} />
        </div>
      </div>
    );
  }
  if (query.isPending) {
    return (
      <div className="flex h-full items-center justify-center gap-2 text-xs text-muted-foreground">
        <Loader2 className="size-4 animate-spin" /> Comparing scene…
      </div>
    );
  }
  if (query.isError) return <MetaDiff diff={diff} detail={`The scene could not be read: ${String((query.error as Error)?.message ?? query.error)}`} />;
  if (!query.data) return <MetaDiff diff={diff} detail="There is no scene preview for this file." />;
  return <SceneCompareView compare={query.data} toolbar={switcher || null} />;
}

const engines: Record<string, string> = { unity: "Unity", godot: "Godot" };

type Node = { object: SceneObject; change?: SceneChange; children: Node[] };

function buildTree(compare: SceneCompare): Node[] {
  const changes = new Map((compare.changes ?? []).map((c) => [c.id, c]));
  const nodes = new Map<string, Node>();
  for (const o of compare.objects ?? []) nodes.set(o.id, { object: o, change: changes.get(o.id), children: [] });
  for (const c of compare.changes ?? []) {
    if (!nodes.has(c.id)) nodes.set(c.id, { object: { id: c.id, path: c.path, type: c.type, parent: "" }, change: c, children: [] });
  }
  const roots: Node[] = [];
  for (const n of nodes.values()) {
    const parent = n.object.parent ? nodes.get(n.object.parent) : undefined;
    if (parent && parent !== n) parent.children.push(n);
    else roots.push(n);
  }
  return roots;
}

function label(node: Node, parent?: SceneObject): string {
  const path = node.object.path;
  if (parent && path.startsWith(parent.path)) {
    const rest = path.slice(parent.path.length).replace(/^(\/| > )/, "");
    if (rest) return rest;
  }
  return path;
}

export function SceneCompareView({ compare, toolbar = null }: { compare: SceneCompare; toolbar?: ReactNode }) {
  const tree = useMemo(() => buildTree(compare), [compare]);
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set());
  const toggle = (id: string) =>
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  const refs = compare.refs ?? [];
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-border px-3 py-1.5 text-[11px]">
        {toolbar}
        <span className="font-medium">{engines[compare.engine] ?? compare.engine}</span>
        <Summary compare={compare} />
      </div>
      {compare.error ? (
        <div className="p-4 text-[11px]">
          <Notice>{`This file cannot be compared as a scene: ${compare.error}.`}</Notice>
        </div>
      ) : (
        <div className="min-h-0 flex-1 overflow-auto bg-card p-2 text-[12px]">
          {tree.length === 0 ? (
            <p className="p-2 text-[11px] text-muted-foreground">No object changes.</p>
          ) : (
            <ul role="tree" aria-label="Scene changes">
              {tree.map((n) => (
                <TreeNode key={n.object.id} node={n} depth={0} collapsed={collapsed} onToggle={toggle} />
              ))}
            </ul>
          )}
          {refs.length > 0 && (
            <section className="mt-3 border-t border-border pt-2" aria-label="Dependency changes">
              <h3 className="px-2 pb-1 text-[11px] font-medium text-muted-foreground">Dependencies</h3>
              <ul>
                {refs.map((r) => (
                  <li key={`${r.status}:${r.path}`} className={cn("truncate px-2 font-mono text-[11px]", r.status === "added" ? "text-success" : "text-danger")}>
                    {r.status === "added" ? "+" : "−"} {r.path}
                  </li>
                ))}
              </ul>
            </section>
          )}
        </div>
      )}
    </div>
  );
}

const badgeTone: Record<string, string> = {
  added: "bg-success/15 text-success",
  removed: "bg-danger/15 text-danger",
  changed: "bg-warning/15 text-warning",
  moved: "bg-accent/15 text-accent",
};

function TreeNode({ node, parent, depth, collapsed, onToggle }: { node: Node; parent?: SceneObject; depth: number; collapsed: Set<string>; onToggle: (id: string) => void }) {
  const { object, change, children } = node;
  const open = !collapsed.has(object.id);
  const expandable = children.length > 0 || (change?.props ?? []).length > 0;
  const Chevron = open ? ChevronDown : ChevronRight;
  return (
    <li role="treeitem" aria-expanded={expandable ? open : undefined} aria-label={object.path} data-status={change?.status ?? "same"}>
      <div className="flex items-center gap-1.5 rounded px-1 py-0.5 hover:bg-muted/50" style={{ paddingLeft: depth * 14 + 4 }}>
        {expandable ? (
          <button type="button" aria-label={open ? `Collapse ${object.path}` : `Expand ${object.path}`} onClick={() => onToggle(object.id)} className="text-muted-foreground hover:text-foreground">
            <Chevron className="size-3.5" />
          </button>
        ) : (
          <span className="w-3.5" />
        )}
        <span className={cn("truncate", change?.status === "removed" && "text-muted-foreground line-through", !change && "text-muted-foreground")} title={object.path}>
          {label(node, parent)}
        </span>
        {object.type && <span className="shrink-0 text-[10px] text-muted-foreground">{object.type}</span>}
        {change && <span className={cn("shrink-0 rounded px-1 text-[10px] font-medium", badgeTone[change.status])}>{change.status}</span>}
      </div>
      {open && (change?.props ?? []).length > 0 && (
        <table className="mb-1 w-full table-fixed text-[11px]" style={{ marginLeft: depth * 14 + 26 }} aria-label={`Properties of ${object.path}`}>
          <tbody>
            {(change?.props ?? []).map((p) => (
              <tr key={p.key} className="align-top">
                <td className="w-2/5 truncate py-0.5 pr-2 font-mono text-muted-foreground" title={p.key}>
                  {p.key}
                </td>
                <td className="truncate py-0.5 font-mono" title={`${p.old} → ${p.new}`}>
                  {change?.status !== "added" && <span className="text-danger line-through">{p.old || "—"}</span>}
                  {change?.status !== "added" && change?.status !== "removed" && <span className="px-1 text-muted-foreground">→</span>}
                  {change?.status !== "removed" && <span className="text-success">{p.new || "—"}</span>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {open && children.length > 0 && (
        <ul role="group">
          {children.map((c) => (
            <TreeNode key={c.object.id} node={c} parent={object} depth={depth + 1} collapsed={collapsed} onToggle={onToggle} />
          ))}
        </ul>
      )}
    </li>
  );
}

const count = (n: number, word: string) => `${n.toLocaleString()} ${word}${n === 1 ? "" : "s"}`;

function Summary({ compare: c }: { compare: SceneCompare }) {
  const parts: { text: string; tone: string }[] = [];
  if (c.added) parts.push({ text: `+${count(c.added, "object")}`, tone: "text-success" });
  if (c.removed) parts.push({ text: `−${count(c.removed, "object")}`, tone: "text-danger" });
  if (c.changed) parts.push({ text: `${c.changed.toLocaleString()} changed`, tone: "text-warning" });
  const refs = (c.refs ?? []).length;
  if (refs) parts.push({ text: count(refs, "dependency change"), tone: "text-accent" });
  if (parts.length === 0 && !c.error) parts.push({ text: "No object changes", tone: "text-muted-foreground" });
  return (
    <span className="flex gap-3 font-medium" aria-label="Scene summary">
      {parts.map((p) => (
        <span key={p.text} className={p.tone}>
          {p.text}
        </span>
      ))}
    </span>
  );
}
