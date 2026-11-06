import { useRef, useState, type KeyboardEvent, type PointerEvent } from "react";
import { cn } from "@/lib/utils";

type ResizeHandleProps = {
  value: number;
  min: number;
  max: number;
  label: string;
  onPreview: (width: number) => void;
  onCommit: (width: number) => void;
  onReset: () => void;
  className?: string;
};

const STEP = 16;
const BIG_STEP = 64;

export function ResizeHandle({ value, min, max, label, onPreview, onCommit, onReset, className }: ResizeHandleProps) {
  const start = useRef<{ x: number; width: number } | null>(null);
  const last = useRef(value);
  const [dragging, setDragging] = useState(false);

  const clamp = (w: number) => Math.min(max, Math.max(min, Math.round(w)));

  const release = () => {
    start.current = null;
    setDragging(false);
    document.body.style.removeProperty("user-select");
    document.body.style.removeProperty("cursor");
  };

  const onPointerDown = (e: PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return;
    e.preventDefault();
    e.currentTarget.setPointerCapture?.(e.pointerId);
    start.current = { x: e.clientX, width: value };
    last.current = value;
    setDragging(true);
    document.body.style.userSelect = "none";
    document.body.style.cursor = "col-resize";
  };

  const onPointerMove = (e: PointerEvent<HTMLDivElement>) => {
    if (!start.current) return;
    last.current = clamp(start.current.width + e.clientX - start.current.x);
    onPreview(last.current);
  };

  const finish = () => {
    if (!start.current) return;
    release();
    onCommit(last.current);
  };

  const onPointerUp = (e: PointerEvent<HTMLDivElement>) => {
    if (e.currentTarget.hasPointerCapture?.(e.pointerId)) e.currentTarget.releasePointerCapture(e.pointerId);
    finish();
  };

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const step = e.shiftKey ? BIG_STEP : STEP;
    const next = { ArrowLeft: value - step, ArrowRight: value + step, Home: min, End: max }[e.key];
    if (next === undefined) return;
    e.preventDefault();
    onCommit(clamp(next));
  };

  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={label}
      aria-valuenow={value}
      aria-valuemin={min}
      aria-valuemax={max}
      tabIndex={0}
      title="Drag to resize, double-click to reset"
      data-dragging={dragging || undefined}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
      onLostPointerCapture={finish}
      onDoubleClick={onReset}
      onKeyDown={onKeyDown}
      className={cn(
        "no-drag group absolute inset-y-0 -right-[3px] z-20 flex w-[6px] cursor-col-resize justify-center outline-none",
        className,
      )}
    >
      <span
        className={cn(
          "h-full w-px bg-transparent transition-colors group-hover:bg-accent group-focus-visible:bg-accent",
          dragging && "bg-accent",
        )}
      />
    </div>
  );
}

type PaneSize = { width: number; min: number; max: number; preview: (w: number) => void; commit: (w: number) => void; reset: () => void };

export function PaneResizer({ size, label }: { size: PaneSize; label: string }) {
  return (
    <ResizeHandle value={size.width} min={size.min} max={size.max} label={label} onPreview={size.preview} onCommit={size.commit} onReset={size.reset} />
  );
}

type ColumnSizes = {
  width: (id: string) => number;
  min: number;
  max: number;
  preview: (id: string, w: number) => void;
  commit: (id: string, w: number) => void;
  reset: (id: string) => void;
};

export function ColumnResizer({ sizes, id, label }: { sizes: ColumnSizes; id: string; label: string }) {
  return (
    <ResizeHandle
      value={sizes.width(id)}
      min={sizes.min}
      max={sizes.max}
      label={`Resize ${label} column`}
      onPreview={(w) => sizes.preview(id, w)}
      onCommit={(w) => sizes.commit(id, w)}
      onReset={() => sizes.reset(id)}
      className="right-0"
    />
  );
}
