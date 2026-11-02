import { cn } from "@/lib/utils";

export function Logo({ className }: { className?: string }) {
  return (
    <svg viewBox="64 64 896 896" className={cn("size-6", className)} aria-hidden="true">
      <defs>
        <linearGradient id="nipa-logo" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#4F46E5" />
          <stop offset="1" stopColor="#9333EA" />
        </linearGradient>
      </defs>
      <rect x="64" y="64" width="896" height="896" rx="200" fill="url(#nipa-logo)" />
      <path d="M352 736V288L672 736V288" fill="none" stroke="#fff" strokeWidth="76" strokeLinecap="round" strokeLinejoin="round" />
      <circle cx="352" cy="736" r="68" fill="#fff" />
      <circle cx="672" cy="288" r="68" fill="#fff" />
    </svg>
  );
}
