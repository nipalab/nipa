import { FolderOpen, Terminal } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Logo } from "@/components/Logo";

export function Welcome({ adding, onAdd }: { adding: boolean; onAdd: () => void }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-6 p-10 text-center">
      <Logo className="size-14" />
      <div className="space-y-2">
        <h1 className="text-xl font-semibold tracking-tight">Welcome to Nipa</h1>
        <p className="max-w-sm text-sm text-muted-foreground">
          Open a clone to see its changes, lock assets and review work with your team.
        </p>
      </div>
      <Button variant="primary" size="lg" onClick={onAdd} disabled={adding}>
        <FolderOpen />
        Open a clone…
      </Button>
      <div className="flex items-center gap-2 rounded-md border border-border bg-card px-3 py-2 text-xs text-muted-foreground">
        <Terminal className="size-3.5" />
        No clone yet? Run <code className="font-mono text-foreground">nipa clone &lt;url&gt; &lt;folder&gt;</code>
      </div>
    </div>
  );
}
