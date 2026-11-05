// "Confirm on this Mac" (elsewhere "this machine"): shown while the daemon asks the OS for the owner's proof (a 428 from a
// gated request). The UI proves nothing itself; it only waits for the daemon's verdict and lets
// the owner withdraw the prompt. With the grace window on, it says that this proof also spares
// the next prompts for a while (every proof opens or renews the window).
// On a paired browser (method "pin") it asks for the owner PIN instead and sends it itself. At
// the machine the OS prompt comes first; when the PIN may answer too, "Use my PIN instead"
// switches to the PIN sheet (the OS prompt stays open: whichever answers first).
import { useState } from "react";
import { Fingerprint, KeyRound, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { cancelConfirmation, graceLabel, submitPin, useGate } from "@/lib/owner-gate";
import { PinInput, validPin } from "@/components/pin-input";
import { useStore } from "@/lib/store";

export function OwnerConfirmDialog() {
  const c = useGate((s) => s.confirming);
  const [pinFor, setPinFor] = useState<string | null>(null); // the confirmation the owner answers with the PIN
  if (c && (c.method === "pin" || (c.pin && pinFor === c.id))) return <PinConfirmDialog key={c.id} reason={c.reason} hello={!!c.hello} />;
  return <OsConfirmDialog onUsePin={c?.pin ? () => setPinFor(c.id) : undefined} />;
}

function PinConfirmDialog({ reason, hello }: { reason: string; hello: boolean }) {
  const [pin, setPin] = useState("");
  const [busy, setBusy] = useState(false);
  const error = useGate((s) => s.pinError);
  const send = async () => {
    if (!validPin(pin) || busy) return;
    setBusy(true);
    try {
      await submitPin(pin);
    } finally {
      setBusy(false);
      setPin("");
    }
  };
  return (
    <Dialog open onOpenChange={(open) => !open && void cancelConfirmation()}>
      <DialogContent className="sm:max-w-[26.25rem]" showCloseButton={false}>
        <form
          className="contents"
          onSubmit={(e) => {
            e.preventDefault();
            void send();
          }}
        >
          <DialogHeader className="items-center text-center sm:text-center">
            <div className="mb-1 flex size-12 items-center justify-center rounded-full border border-brand/25 bg-brand/10">
              <KeyRound className="size-6 text-brand" />
            </div>
            <DialogTitle>Enter your owner PIN</DialogTitle>
            <DialogDescription className="text-balance">
              tmx is trying to <span className="text-foreground">{(reason || "do something only you may do").replace(/-/g, "‑")}</span>.
            </DialogDescription>
          </DialogHeader>
          <PinInput value={pin} onChange={setPin} autoFocus disabled={busy} />
          {hello && <p className="text-center text-xs text-muted-foreground">or confirm with Windows Hello / Touch ID on this machine</p>}
          {error && <p role="alert" className="text-center text-xs text-destructive">{error}</p>}
          <DialogFooter className="sm:justify-center">
            <Button type="button" variant="ghost" size="sm" onClick={() => void cancelConfirmation()}>
              Cancel
            </Button>
            <Button type="submit" size="sm" disabled={!validPin(pin) || busy}>
              {busy ? <Loader2 className="size-3.5 animate-spin" /> : "Confirm"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function OsConfirmDialog({ onUsePin }: { onUsePin?: () => void }) {
  const c = useGate((s) => s.confirming);
  const platform = useStore((s) => s.transport?.platform);
  const where = platform === "darwin" || !platform ? "this Mac" : "this machine";
  const label = c?.method_label || (platform === "windows" ? "Windows Hello" : "Touch ID or your Mac password");
  const grace = useGate((s) => s.graceAsked ?? s.safety?.gate.grace_minutes ?? 0);
  return (
    <Dialog open={!!c} onOpenChange={(open) => !open && void cancelConfirmation()}>
      <DialogContent className="sm:max-w-[26.25rem]" showCloseButton={false}>
        <DialogHeader className="items-center text-center sm:text-center">
          <div className="mb-1 flex size-12 items-center justify-center rounded-full border border-brand/25 bg-brand/10">
            <Fingerprint className="size-6 text-brand" />
          </div>
          <DialogTitle>Confirm on {where}</DialogTitle>
          <DialogDescription className="text-balance">
            Use {label} in the system prompt. tmx is trying to <span className="text-foreground">{(c?.reason || "do something only you may do").replace(/-/g, "‑")}</span>.
          </DialogDescription>
        </DialogHeader>
        <div className="flex items-center justify-center gap-2 rounded-lg border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
          <Loader2 className="size-3.5 animate-spin" />
          Waiting for your answer on {where}…
        </div>
        {grace > 0 && (
          <p className="text-center text-xs text-balance text-muted-foreground">
            Tiamatrix won't ask again for {graceLabel(grace)} on {where} (Settings → Safety).
          </p>
        )}
        <DialogFooter className="sm:justify-center">
          <Button variant="ghost" size="sm" onClick={() => void cancelConfirmation()}>
            Cancel
          </Button>
          {onUsePin && (
            <Button variant="outline" size="sm" onClick={onUsePin}>
              <KeyRound className="size-3.5" />
              Use my PIN instead
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
