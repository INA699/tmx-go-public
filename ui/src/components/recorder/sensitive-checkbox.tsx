// The one "sensitive — keep on this machine" checkbox (lib/sensitive.ts): in the recording options, and on a
// recording while it runs and on its report. Owner only (the window is the owner's).
import { Checkbox } from "@/components/ui/checkbox";
import { SENSITIVE_HINT, SENSITIVE_LABEL } from "@/lib/sensitive";

export function SensitiveCheckbox({ checked, onChange, disabled, id = "rec-sensitive" }: {
  checked: boolean;
  onChange: (on: boolean) => void;
  disabled?: boolean;
  id?: string;
}) {
  return (
    <label htmlFor={id} className="flex items-start gap-2 text-sm" data-sensitive>
      <Checkbox id={id} checked={checked} disabled={disabled} onCheckedChange={(v) => onChange(v === true)} className="mt-0.5" />
      <span className="grid gap-0.5">
        <span>{SENSITIVE_LABEL}</span>
        <span className="text-xs text-muted-foreground">{SENSITIVE_HINT}</span>
      </span>
    </label>
  );
}
