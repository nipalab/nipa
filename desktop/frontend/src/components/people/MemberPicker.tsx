import { useEffect, useId, useRef, useState, type KeyboardEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { Check, Loader2, X } from "lucide-react";
import { desktop, type Person } from "@/api/desktop";
import { Avatar } from "@/components/people/Avatar";
import { useDebounced } from "@/hooks/useDebounced";
import { cn, errorMessage } from "@/lib/utils";

const SEARCH_DEBOUNCE_MS = 200;

type Props = {
  root: string;
  label: string;
  selected: Person[];
  onChange: (next: Person[]) => void;
  exclude?: string[];
  placeholder?: string;
  disabled?: boolean;
};

// MemberPicker searches the people who can read the project and keeps a
// multi-selection; the chips are removable and the list works from the keyboard.
export function MemberPicker({ root, label, selected, onChange, exclude = [], placeholder = "Search people", disabled }: Props) {
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const box = useRef<HTMLDivElement>(null);
  const listId = useId();
  const debounced = useDebounced(query.trim(), SEARCH_DEBOUNCE_MS);
  const members = useQuery({ queryKey: ["members", root, debounced], queryFn: () => desktop.members(root, debounced), enabled: open, staleTime: 30_000 });
  const results = (members.data ?? []).filter((p) => !exclude.includes(p.id));
  const picked = new Set(selected.map((p) => p.id));
  useEffect(() => setActive(0), [debounced, results.length]);

  const toggle = (person: Person) => onChange(picked.has(person.id) ? selected.filter((p) => p.id !== person.id) : [...selected, person]);
  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.nativeEvent.isComposing) return;
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      setOpen(true);
      if (results.length > 0) setActive((i) => (i + (e.key === "ArrowDown" ? 1 : results.length - 1)) % results.length);
    } else if (e.key === "Enter") {
      e.preventDefault();
      if (open && results[active]) toggle(results[active]);
    } else if (e.key === "Escape" && open) {
      e.stopPropagation();
      setOpen(false);
    } else if (e.key === "Backspace" && query === "" && selected.length > 0) {
      onChange(selected.slice(0, -1));
    }
  };

  return (
    <div
      ref={box}
      className="relative"
      onBlur={(e) => {
        if (!box.current?.contains(e.relatedTarget)) setOpen(false);
      }}
    >
      <div className={cn("flex min-h-8 flex-wrap items-center gap-1 rounded-md border border-border bg-background px-1.5 py-1 focus-within:border-accent", disabled && "opacity-60")}>
        {selected.map((p) => (
          <span key={p.id} className="inline-flex items-center gap-1 rounded-full bg-muted py-0.5 pl-0.5 pr-1.5 text-[11px]">
            <Avatar name={p.name} id={p.id} className="size-4 text-[8px]" />
            {p.name}
            <button type="button" aria-label={`Remove ${p.name}`} disabled={disabled} onClick={() => toggle(p)} className="text-muted-foreground hover:text-foreground">
              <X className="size-3" />
            </button>
          </span>
        ))}
        <input
          value={query}
          disabled={disabled}
          onChange={(e) => {
            setQuery(e.target.value);
            setOpen(true);
          }}
          onFocus={() => setOpen(true)}
          onKeyDown={onKeyDown}
          role="combobox"
          aria-label={label}
          aria-expanded={open}
          aria-controls={listId}
          aria-autocomplete="list"
          aria-activedescendant={open && results[active] ? `${listId}-${results[active].id}` : undefined}
          placeholder={selected.length === 0 ? placeholder : ""}
          spellCheck={false}
          className="min-w-20 flex-1 bg-transparent px-1 text-[12px] outline-none placeholder:text-muted-foreground"
        />
        {open && members.isFetching && <Loader2 className="size-3 animate-spin text-muted-foreground" aria-label="Searching" />}
      </div>
      {open && (
        <div id={listId} role="listbox" aria-label={`${label} results`} aria-multiselectable className="absolute inset-x-0 top-full z-50 mt-1 max-h-48 overflow-y-auto rounded-md border border-border bg-card py-1 shadow-lg">
          {members.isError ? (
            <p role="alert" className="px-3 py-2 text-[11px] text-danger">
              {errorMessage(members.error)}
            </p>
          ) : results.length === 0 ? (
            !members.isFetching && <p className="px-3 py-2 text-[11px] text-muted-foreground">{debounced ? "Nobody with that name can read this project" : "No other people can read this project yet"}</p>
          ) : (
            results.map((p, i) => (
              <div
                key={p.id}
                id={`${listId}-${p.id}`}
                role="option"
                tabIndex={-1}
                aria-selected={picked.has(p.id)}
                onMouseDown={(e) => e.preventDefault()}
                onMouseMove={() => i !== active && setActive(i)}
                onClick={() => toggle(p)}
                className={cn("mx-1 flex cursor-default items-center gap-2 rounded px-2 py-1 text-[12px]", i === active && "bg-accent-soft")}
              >
                <Avatar name={p.name} id={p.id} />
                <span className="min-w-0 flex-1 truncate">{p.name}</span>
                {picked.has(p.id) && <Check className="size-3.5 text-accent" aria-hidden />}
              </div>
            ))
          )}
        </div>
      )}
    </div>
  );
}
