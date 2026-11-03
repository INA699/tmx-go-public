// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

// Settings → Safety: the dangerous-command guard (on/off, per level), its rules, and the owner
// gate with its grace window (ARCHITECTURE.md "Safety"). Changes that protect less go through the owner gate.
import { useEffect, useState } from "react";
import { AlertTriangle, Ban, ChevronRight, Fingerprint, KeyRound, LifeBuoy, Lock, LockOpen, ShieldAlert, ShieldCheck } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Skeleton } from "@/components/ui/skeleton";
import { Slider } from "@/components/ui/slider";
import { Switch } from "@/components/ui/switch";
import { CopyButton } from "@/components/copy-button";
import { PinInput, validPin } from "@/components/pin-input";
import { Section } from "@/components/page";
import { api } from "@/lib/api";
import { displayPath, LEVEL_LABEL } from "@/lib/format";
import { GRACE_STEPS, graceLabel, graceTick, graceUntilLabel, loadSafety, setGateStatus, useGate, useGraceUntil } from "@/lib/owner-gate";
import type { GateStatus, GuardRule, GuardSettings, Level, PinStatus } from "@/lib/types";
import { cn } from "@/lib/utils";

const LEVELS: Level[] = ["head", "lead", "worker"];

function Card({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cn("rounded-xl border bg-card", className)}>{children}</div>;
}

function ToggleRow({
  id,
  label,
  hint,
  checked,
  disabled,
  onChange,
}: {
  id: string;
  label: React.ReactNode;
  hint?: React.ReactNode;
  checked: boolean;
  disabled?: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="flex items-start gap-4 px-4 py-3">
      <label htmlFor={id} className="min-w-0 flex-1 cursor-pointer">
        <div className="text-sm">{label}</div>
        {hint && <div className="mt-0.5 text-xs text-muted-foreground">{hint}</div>}
      </label>
      <Switch id={id} checked={checked} disabled={disabled} onCheckedChange={onChange} className="mt-0.5" />
    </div>
  );
}

function GuardCard({ g }: { g: GuardSettings }) {
  const [busy, setBusy] = useState(false);

  async function save(next: GuardSettings, done: string) {
    setBusy(true);
    try {
      const saved = await api.putGuard(next);
      useGate.setState((s) => (s.safety ? { safety: { ...s.safety, guard: saved ?? next } } : {}));
      toast.success(done, { description: "Running agents keep their current setting until they restart." });
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  const offFor = new Set(g.off_for ?? []);
  return (
    <Card className="divide-y">
      <ToggleRow
        id="guard-on"
        label={
          <span className="flex items-center gap-1.5 font-medium">
            {g.enabled ? <ShieldCheck className="size-4 text-state-ok" /> : <ShieldAlert className="size-4 text-state-error" />}
            Dangerous-command guard
          </span>
        }
        hint="Checks every command and file write of chat-mode Claude agents, in every permission mode, including Bypass permissions."
        checked={g.enabled}
        disabled={busy}
        onChange={(on) => void save({ ...g, enabled: on }, on ? "Guard switched on" : "Guard switched off")}
      />
      {!g.enabled && (
        <div className="flex items-start gap-2 bg-diff-del-bg px-4 py-2.5 text-xs">
          <AlertTriangle className="mt-px size-3.5 shrink-0 text-state-error" />
          <span>Nothing stops an agent from wiping a disk, deleting your home folder or killing other agents' tools.</span>
        </div>
      )}
      {LEVELS.map((l) => (
        <ToggleRow
          key={l}
          id={"guard-" + l}
          label={`${LEVEL_LABEL[l]} agents`}
          checked={g.enabled && !offFor.has(l)}
          disabled={busy || !g.enabled}
          onChange={(on) => {
            const next = new Set(offFor);
            if (on) next.delete(l);
            else next.add(l);
            void save({ ...g, off_for: [...next] }, on ? `Guard on for ${l}s` : `Guard off for ${l}s`);
          }}
        />
      ))}
      <p className="px-4 py-2.5 text-xs text-muted-foreground">
        Terminal-mode agents are not covered: the guard cannot see what is typed into a terminal.
      </p>
    </Card>
  );
}

function RuleRow({ r }: { r: GuardRule }) {
  const [open, setOpen] = useState(false);
  const block = r.outcome === "block";
  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <CollapsibleTrigger className="flex w-full items-start gap-3 px-4 py-2.5 text-left outline-none hover:bg-accent/40 focus-visible:bg-accent/40">
        {block ? <Ban className="mt-0.5 size-3.5 shrink-0 text-state-error" /> : <ShieldAlert className="mt-0.5 size-3.5 shrink-0 text-[color-mix(in_oklch,var(--state-waiting)_80%,var(--foreground))]" />}
        <span className="min-w-0 flex-1">
          <span className="block text-[13px] font-medium">{r.name}</span>
          <span className="block text-xs text-muted-foreground">{r.summary}</span>
        </span>
        <ChevronRight className={cn("mt-0.5 size-3.5 shrink-0 text-subtle-foreground transition-transform duration-150", open && "rotate-90")} />
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className="space-y-2 px-4 pb-3 pl-10.5">
          <p className="text-xs leading-relaxed text-foreground/85">{r.why}</p>
          {r.examples?.length > 0 && (
            <div className="flex flex-wrap gap-1">
              {r.examples.map((x) => (
                <code key={x} className="rounded-md border bg-code px-1.5 py-0.5 font-mono text-[11.5px] text-foreground/80">
                  {x}
                </code>
              ))}
            </div>
          )}
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}

function RulesCard({ rules }: { rules: GuardRule[] }) {
  const groups: { title: string; hint: string; items: GuardRule[] }[] = [
    { title: "Blocked", hint: "Never runs; the agent is told why.", items: rules.filter((r) => r.outcome === "block") },
    { title: "Held for you", hint: "Waits in Needs you for Allow once or Deny; no answer in 10 minutes means no.", items: rules.filter((r) => r.outcome === "hold") },
  ];
  return (
    <div className="space-y-3">
      {groups.map((gr) => (
        <Card key={gr.title} className="overflow-hidden">
          <div className="flex items-baseline gap-2 border-b px-4 py-2.5">
            <h3 className="text-[13px] font-semibold">{gr.title}</h3>
            <p className="text-xs text-muted-foreground">{gr.hint}</p>
          </div>
          <div className="divide-y">
            {gr.items.map((r) => (
              <RuleRow key={r.id} r={r} />
            ))}
          </div>
        </Card>
      ))}
      <p className="text-xs text-muted-foreground">Everything else runs as usual; the agent's permission mode still applies.</p>
    </div>
  );
}

/**
 * The grace window: after a proof the daemon stops asking for this long (a dial of fixed steps).
 * Longer asks for a fresh proof (the sheet then names the new length); shorter, Off and Lock now
 * never do. The window lives in the daemon's memory, so a restart locks it.
 */
function GraceRow({ gate }: { gate: GateStatus }) {
  const steps = gate.grace_steps?.length ? gate.grace_steps : GRACE_STEPS;
  const minutes = gate.grace_minutes ?? 0;
  const until = useGraceUntil();
  const [draft, setDraft] = useState<number | null>(null); // the step being dragged or saved
  const [busy, setBusy] = useState(false);
  const at = steps.indexOf(minutes);
  const idx = draft ?? (at >= 0 ? at : 0);
  const shown = steps[idx] ?? 0;
  const disabled = busy || !gate.available;

  async function save(i: number) {
    const next = steps[i];
    if (next === undefined || next === minutes) {
      setDraft(null);
      return;
    }
    setDraft(i);
    setBusy(true);
    if (next > minutes) useGate.setState({ graceAsked: next });
    try {
      const st = await api.putGrace(next);
      if (st) setGateStatus(st);
      toast.success(next === 0 ? "tmx asks every time" : `No prompt for ${graceLabel(next)} after a confirmation`);
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
      setDraft(null);
      useGate.setState({ graceAsked: null });
    }
  }

  async function lock() {
    setBusy(true);
    try {
      setGateStatus(await api.lockGate());
      toast.success("Locked: the next action asks again");
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="px-4 py-3" data-testid="grace">
      <div className="flex items-baseline gap-2">
        <div className="text-sm">Don't ask again for</div>
        <div className="ml-auto text-sm font-medium tabular-nums">{graceLabel(shown)}</div>
      </div>
      <p className="mt-0.5 text-xs text-muted-foreground">
        After you confirm once, tmx skips the prompt for this long. You still decide each action.
      </p>
      <div className="mt-3.5">
        <Slider
          min={0}
          max={steps.length - 1}
          step={1}
          value={[idx]}
          disabled={disabled}
          onValueChange={([i]) => setDraft(i)}
          onValueCommit={([i]) => void save(i)}
          thumbLabel="Don't ask again for"
          valueText={graceLabel(shown)}
        />
        <div className="mt-1.5 flex h-4 justify-between px-2">
          {steps.map((m, i) => (
            <span key={m} className="relative w-0">
              <button
                type="button"
                tabIndex={-1}
                disabled={disabled}
                onClick={() => void save(i)}
                className={cn(
                  "absolute left-1/2 -translate-x-1/2 px-0.5 text-2xs whitespace-nowrap text-muted-foreground tabular-nums outline-none hover:text-foreground disabled:pointer-events-none",
                  i === idx && "font-medium text-foreground",
                )}
              >
                {graceTick(m)}
              </button>
            </span>
          ))}
        </div>
      </div>
      <div className="mt-3 flex min-h-9 flex-wrap items-center gap-x-2 gap-y-1.5 rounded-lg border bg-muted/40 px-3 py-1.5 text-xs">
        {until ? (
          <>
            <LockOpen className="size-3.5 shrink-0 text-brand" />
            <span>
              No confirmation needed until <span className="font-medium tabular-nums">{graceUntilLabel(until)}</span>
            </span>
            <Button size="xs" variant="outline" className="ml-auto" disabled={busy} onClick={() => void lock()}>
              <Lock />
              Lock now
            </Button>
          </>
        ) : (
          <>
            <Lock className="size-3.5 shrink-0 text-subtle-foreground" />
            <span className="text-muted-foreground">{minutes === 0 ? "Asks every time." : "Locked: the next action asks."}</span>
          </>
        )}
      </div>
      <p className="mt-2 text-xs text-muted-foreground">
        {gate.available ? "" : "Needs a way to confirm on this machine. "}
        Kept in memory only: a restart or update locks it. Only this UI session can use its window. Turning the gate on or off, and a
        longer window, always ask.
      </p>
    </div>
  );
}

function GateCard() {
  const gate = useGate((s) => s.safety?.gate);
  const [busy, setBusy] = useState(false);
  if (!gate) return null;
  const remote = gate.method === "pin";

  async function toggle(on: boolean) {
    setBusy(true);
    try {
      const st = await api.putGate(on);
      if (st) setGateStatus(st);
      toast.success(on ? "Owner gate on" : "Owner gate off");
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card className="divide-y">
      <div className="flex flex-col gap-3 px-4 py-3 sm:flex-row sm:items-start">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5 text-sm font-medium">
            <Fingerprint className={cn("size-4", gate.enabled ? "text-brand" : "text-subtle-foreground")} />
            Owner gate
            <span
              className={cn(
                "ml-1 rounded-md border px-1.5 py-px text-2xs font-medium",
                gate.enabled ? "border-brand/30 bg-brand/10 text-foreground" : "text-muted-foreground",
              )}
            >
              {gate.enabled ? "On" : "Off"}
            </span>
          </div>
          <p className="mt-0.5 text-xs text-muted-foreground">
            {remote ? "Owner actions ask for your owner PIN on this device. Change the gate switch and grace settings on the host computer." : gate.available
              ? `The daemon asks for ${gate.method_label} on this machine before the actions below. Once enrolled, missing state keeps it on; turning it on or off asks once too.`
              : `Not available here: ${gate.unavailable || "no way to ask the owner on this machine"}.`}
          </p>
        </div>
        <Button
          size="sm"
          variant={gate.enabled ? "outline" : "default"}
          disabled={remote || busy || (!gate.enabled && !gate.available)}
          onClick={() => void toggle(!gate.enabled)}
          className="shrink-0"
        >
          <Fingerprint />
          {gate.enabled ? "Turn off" : "Turn on"}
        </Button>
      </div>
      {!remote && gate.grace_steps && <GraceRow gate={gate} />}
      <div className="px-4 py-3">
        <div className="mb-1.5 text-xs font-medium text-muted-foreground">{gate.enabled ? "Asks before" : "Will ask before"}</div>
        <ul className="grid gap-1 text-[13px] sm:grid-cols-2">
          {gate.gated.map((x) => (
            <li key={x} className="flex items-start gap-1.5">
              <span className="mt-[7px] size-1 shrink-0 rounded-full bg-subtle-foreground" />
              {x}
            </li>
          ))}
        </ul>
      </div>
      <div className="flex items-start gap-2.5 px-4 py-3 text-xs text-muted-foreground">
        <LifeBuoy className="mt-px size-3.5 shrink-0" />
        <div className="min-w-0 space-y-1">
          <p>
            {gate.recovery || "The gate protects tmx actions, never your computer login or files. See the documented offline recovery procedure if confirmation stops working."}
          </p>
          {gate.file && (
            <div className="flex min-w-0 items-center gap-1">
              <code className="truncate font-mono text-[11.5px] text-foreground/85" title={gate.file}>
                {displayPath(gate.file)}
              </code>
              <CopyButton text={gate.file} label="Copy path" />
            </div>
          )}
        </div>
      </div>
    </Card>
  );
}

// The owner PIN: ONE short code (6 to 12 digits) for every owner step from a phone or another
// browser, the same on every machine and in the Python tmx. Set it here at the machine; from
// a browser it changes only with the current one. Wrong tries slow down, never lock out.
function PinCard() {
  const [st, setSt] = useState<PinStatus | null>(null);
  const [pin, setPin] = useState("");
  const [again, setAgain] = useState("");
  const [old, setOld] = useState("");
  const [busy, setBusy] = useState(false);
  const load = () => api.pinStatus().then(setSt, () => setSt(null));
  useEffect(() => {
    void load();
  }, []);
  if (!st?.available) return null;
  const here = !!st.at_machine;
  const needOld = st.set && !here;
  const ok = validPin(pin) && pin === again && (!needOld || validPin(old));

  async function save() {
    setBusy(true);
    try {
      await api.setPin(pin, needOld ? old : undefined);
      toast.success(st?.set ? "Owner PIN changed on this machine (the others pick it up within 5 min)" : "Owner PIN set");
      setPin("");
      setAgain("");
      setOld("");
      await load();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  async function clearWaits() {
    try {
      await api.clearPinWaits();
      toast.success("Wrong-PIN waits cleared");
      await load();
    } catch (e) {
      toast.error((e as Error).message);
    }
  }

  return (
    <Card className="divide-y">
      <div className="px-4 py-3">
        <div className="flex items-center gap-1.5 text-sm font-medium">
          <KeyRound className={cn("size-4", st.set ? "text-brand" : "text-subtle-foreground")} />
          Owner PIN
          <span className="ml-1 rounded-md border px-1.5 py-px text-2xs font-medium text-muted-foreground">{st.set ? "Set" : "Not set"}</span>
        </div>
        <p className="mt-0.5 text-xs text-muted-foreground">
          One short code for approvals and pairing from your phone or another browser: the same on all your machines. Wrong tries make you wait a little, never lock you out.
          {!here && !st.set && " Set it on the machine itself."}
          {!here && st.set && " Lost it? Set a new one on the machine itself."}
        </p>
        {st.error && <p className="mt-1 text-xs text-destructive">{st.error}</p>}
        {(here || st.set) && (
          <form
            className="mt-3 grid max-w-xs gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              if (ok) void save();
            }}
          >
            {needOld && <PinInput label="Current PIN" value={old} onChange={setOld} />}
            <PinInput label={st.set ? "New PIN" : "PIN"} value={pin} onChange={setPin} />
            <PinInput label="The same PIN again" value={again} onChange={setAgain} />
            {again && pin !== again && <p className="text-xs text-destructive">The two PINs differ.</p>}
            <div className="flex gap-2">
              <Button type="submit" size="sm" disabled={!ok || busy}>
                {st.set ? "Change PIN" : "Set PIN"}
              </Button>
              {here && st.set && (
                <Button type="button" size="sm" variant="outline" onClick={() => void clearWaits()}>
                  Clear wrong-PIN waits
                </Button>
              )}
            </div>
          </form>
        )}
      </div>
    </Card>
  );
}

export function SafetySection() {
  const safety = useGate((s) => s.safety);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let live = true;
    void loadSafety().then((s) => live && setFailed(!s));
    return () => {
      live = false;
    };
  }, []);

  return (
    <Section title="Safety">
      <p className="-mt-1 mb-3 text-xs text-muted-foreground">What agents may never do, what waits for you, and how tmx makes sure it is you.</p>
      {!safety ? (
        failed ? (
          <Card className="px-4 py-3 text-sm text-muted-foreground">This daemon doesn't report its safety settings. Update tmx.</Card>
        ) : (
          <div className="space-y-3">
            <Skeleton className="h-40 rounded-xl" />
            <Skeleton className="h-24 rounded-xl" />
          </div>
        )
      ) : (
        <div className="space-y-6">
          <GuardCard g={safety.guard} />
          <div>
            <h3 className="mb-2 text-xs font-medium text-muted-foreground">Rules</h3>
            <RulesCard rules={safety.rules} />
          </div>
          <div>
            <h3 className="mb-2 text-xs font-medium text-muted-foreground">Confirmation</h3>
            <GateCard />
          </div>
          <div>
            <h3 className="mb-2 text-xs font-medium text-muted-foreground">Owner PIN</h3>
            <PinCard />
          </div>
        </div>
      )}
    </Section>
  );
}
