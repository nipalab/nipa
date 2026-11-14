import { hueFor, initials } from "@/lib/people";
import { cn } from "@/lib/utils";

export function Avatar({ name, id = name, className }: { name: string; id?: string; className?: string }) {
  return (
    <span
      aria-hidden
      className={cn("inline-flex size-5 shrink-0 items-center justify-center rounded-full text-[9px] font-semibold text-white", className)}
      style={{ backgroundColor: `hsl(${hueFor(id)} 42% 42%)` }}
    >
      {initials(name)}
    </span>
  );
}
