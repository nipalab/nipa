import { Loader2, RotateCw, ServerCrash } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Logo } from "@/components/Logo";

export function Splash({ label }: { label: string }) {
  return (
    <div className="drag-region flex h-full flex-col items-center justify-center gap-4" role="status">
      <Logo className="size-10" />
      <span className="flex items-center gap-2 text-xs text-muted-foreground">
        <Loader2 className="size-3.5 animate-spin" />
        {label}
      </span>
    </div>
  );
}

export function DaemonError({ message, retrying, onRetry }: { message: string; retrying: boolean; onRetry: () => void }) {
  return (
    <div className="drag-region flex h-full items-center justify-center p-10">
      <div className="w-full max-w-md space-y-4 rounded-xl border border-border bg-card p-6 shadow-sm" role="alert">
        <div className="flex items-center gap-3">
          <div className="flex size-9 items-center justify-center rounded-lg bg-danger-soft">
            <ServerCrash className="size-5 text-danger" />
          </div>
          <div>
            <h1 className="text-sm font-semibold">Can't reach the Nipa daemon</h1>
            <p className="text-xs text-muted-foreground">The app talks to your clones through `nipa serve`.</p>
          </div>
        </div>
        <pre className="select-text whitespace-pre-wrap break-words rounded-md bg-muted p-3 font-mono text-[11px]">{message}</pre>
        <ul className="list-disc space-y-1 pl-4 text-xs text-muted-foreground">
          <li>Make sure the `nipa` CLI is installed next to the app or on your PATH.</li>
          <li>Or point the app at it with the NIPA_BIN environment variable.</li>
        </ul>
        <Button variant="primary" onClick={onRetry} disabled={retrying} className="w-full">
          <RotateCw className={retrying ? "animate-spin" : undefined} />
          Try again
        </Button>
      </div>
    </div>
  );
}
