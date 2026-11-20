import { useId, type ReactNode } from "react";
import { cn } from "@/lib/utils";

export const fieldClass = "h-8 rounded-md border border-border bg-background px-2.5 text-[12px] outline-none transition-colors focus:border-accent aria-[invalid=true]:border-danger";

export function Group({ title, children, footer }: { title?: string; children: ReactNode; footer?: ReactNode }) {
  return (
    <div className="space-y-2">
      {title && <h4 className="px-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">{title}</h4>}
      <div className="divide-y divide-border rounded-lg border border-border bg-background/40">{children}</div>
      {footer && <div className="px-1 text-muted-foreground">{footer}</div>}
    </div>
  );
}

type RowProps = {
  label: ReactNode;
  description?: ReactNode;
  htmlFor?: string;
  labelId?: string;
  children?: ReactNode;
  below?: ReactNode;
  tone?: "default" | "danger" | "accent";
};

export function Row({ label, description, htmlFor, labelId, children, below, tone = "default" }: RowProps) {
  const Label = htmlFor ? "label" : "div";
  return (
    <div className="px-4 py-3">
      <div className="flex items-center justify-between gap-6">
        <div className="min-w-0">
          <Label id={labelId} htmlFor={htmlFor} className="block font-medium text-foreground">
            {label}
          </Label>
          {description && <div className={cn("mt-0.5 leading-relaxed", tone === "danger" ? "text-danger" : tone === "accent" ? "text-accent" : "text-muted-foreground")}>{description}</div>}
        </div>
        {children && <div className="flex shrink-0 items-center gap-2">{children}</div>}
      </div>
      {below}
    </div>
  );
}

export function Switch({ checked, onChange, labelledBy }: { checked: boolean; onChange: (v: boolean) => void; labelledBy: string }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-labelledby={labelledBy}
      onClick={() => onChange(!checked)}
      className={cn(
        "relative inline-flex h-5 w-9 shrink-0 items-center rounded-full border transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        checked ? "border-accent bg-accent" : "border-transparent bg-muted-foreground/35",
      )}
    >
      <span className={cn("inline-block size-3.5 rounded-full bg-white shadow transition-transform", checked ? "translate-x-[18px]" : "translate-x-[2px]")} />
    </button>
  );
}

export function SwitchRow({ label, description, checked, onChange }: { label: string; description?: ReactNode; checked: boolean; onChange: (v: boolean) => void }) {
  const id = useId();
  return (
    <Row label={label} description={description} labelId={id}>
      <Switch checked={checked} onChange={onChange} labelledBy={id} />
    </Row>
  );
}
