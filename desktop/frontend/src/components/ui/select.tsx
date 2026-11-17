import { useEffect, useId, useRef, useState, type KeyboardEvent } from "react";
import { Check, ChevronDown } from "lucide-react";
import { cn } from "@/lib/utils";

export type SelectOption<T extends string> = { id: T; label: string; hint?: string };

type SelectProps<T extends string> = {
  value: T;
  options: readonly SelectOption<T>[];
  onChange: (value: T) => void;
  label: string;
  disabled?: boolean;
  className?: string;
};

export function Select<T extends string>({ value, options, onChange, label, disabled, className }: SelectProps<T>) {
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const root = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const listId = useId();
  const selected = options.find((o) => o.id === value) ?? options[0];

  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => {
      if (!root.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", close);
    return () => document.removeEventListener("mousedown", close);
  }, [open]);

  const show = () => {
    setActive(Math.max(0, options.findIndex((o) => o.id === value)));
    setOpen(true);
  };
  const pick = (id: T) => {
    onChange(id);
    setOpen(false);
    button.current?.focus();
  };
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key === "Escape" && open) {
      e.preventDefault();
      e.stopPropagation();
      setOpen(false);
      return;
    }
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (!open) return show();
      const step = e.key === "ArrowDown" ? 1 : -1;
      setActive((i) => (i + step + options.length) % options.length);
      return;
    }
    if ((e.key === "Enter" || e.key === " ") && open) {
      e.preventDefault();
      pick(options[active].id);
    }
  };

  return (
    <div ref={root} className={cn("relative", className)}
      onKeyDown={onKeyDown}
      onBlur={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setOpen(false);
      }}
    >
      <button
        ref={button}
        type="button"
        role="combobox"
        aria-label={label}
        aria-expanded={open}
        aria-controls={listId}
        aria-activedescendant={open ? `${listId}-${active}` : undefined}
        disabled={disabled}
        title={selected?.hint}
        onClick={() => (open ? setOpen(false) : show())}
        className="no-drag inline-flex h-7 items-center gap-1.5 rounded-md border border-border bg-background px-2 text-[12px] text-foreground shadow-sm transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50"
      >
        {selected?.label}
        <ChevronDown className={cn("size-3.5 text-muted-foreground transition-transform", open && "rotate-180")} />
      </button>
      {open && (
        <ul id={listId} role="listbox" aria-label={label} className="absolute left-0 top-full z-50 mt-1 w-64 overflow-hidden rounded-md border border-border bg-card py-1 shadow-lg">
          {options.map((o, i) => (
            <li
              key={o.id}
              id={`${listId}-${i}`}
              role="option"
              aria-selected={o.id === value}
              onMouseEnter={() => setActive(i)}
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => pick(o.id)}
              className={cn("flex cursor-pointer gap-2 px-2.5 py-1.5", i === active && "bg-muted")}
            >
              <Check className={cn("mt-0.5 size-3.5 shrink-0 text-accent", o.id !== value && "invisible")} />
              <span className="min-w-0">
                <span className="block text-[12px] font-medium text-foreground">{o.label}</span>
                {o.hint && <span className="block text-[11px] leading-snug text-muted-foreground">{o.hint}</span>}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
