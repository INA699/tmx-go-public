// Tiamatrix's mark: the app icon (build/appicon.svg, copied to public/favicon.svg by
// `make icon`), so the header, the favicon and the desktop icon are one picture.
import { cn } from "@/lib/utils";

export function BrandMark({ className }: { className?: string }) {
  return <img src="/favicon.svg" alt="Tiamatrix" draggable={false} className={cn("inline-block size-6 shrink-0 select-none", className)} />;
}
