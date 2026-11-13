import { useEffect, useRef } from "react";
import { matchShortcut, type ShortcutId, type ShortcutMatch } from "@/lib/shortcuts";

export type ShortcutHandlers = Partial<Record<ShortcutId, (match: ShortcutMatch, e: KeyboardEvent) => void>>;

// useShortcuts runs the handlers for the shortcuts this component owns; keys
// it has no handler for pass through untouched.
export function useShortcuts(handlers: ShortcutHandlers, enabled = true) {
  const ref = useRef(handlers);
  ref.current = handlers;
  useEffect(() => {
    if (!enabled) return;
    const onKey = (e: KeyboardEvent) => {
      const match = matchShortcut(e);
      const handler = match && ref.current[match.id];
      if (!match || !handler) return;
      e.preventDefault();
      handler(match, e);
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [enabled]);
}
