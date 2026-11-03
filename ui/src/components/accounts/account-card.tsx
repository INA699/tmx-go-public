// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

// One AI account on the Usage page: who is signed in, its quotas, and Connect / Sign out.
import { useState } from "react";
import { AlertTriangle, Info, Loader2, LogIn, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { accountsApi, methodLabel, sentence, type AccountStatus } from "@/lib/accounts";
import { relativeTime } from "@/lib/format";
import { cn } from "@/lib/utils";
import { HarnessMark, InstallHint, LimitRow } from "./usage-bits";

const AMBER = "text-[color-mix(in_oklch,var(--state-waiting)_72%,var(--foreground))]";

function refresh(a: AccountStatus) {
  accountsApi.refresh(a.harness).catch((e) => toast.error(`Could not check ${a.label}`, { description: (e as Error).message }));
}

export function AccountCard({
  a,
  now,
  starting,
  onConnect,
}: {
  a: AccountStatus;
  now: number;
  /** Connect was clicked and the daemon has not answered yet */
  starting: boolean;
  /** start the sign-in, or show the one in progress */
  onConnect: (a: AccountStatus) => void;
}) {
  const [askOut, setAskOut] = useState(false);
  const [signingOut, setSigningOut] = useState(false);
  const id = a.account ?? {};
  const method = methodLabel(id.method);
  const limits = a.limits ?? [];
  const login = a.login.state;

  const signOut = async () => {
    setSigningOut(true);
    try {
      await accountsApi.logout(a.harness);
      toast(`Signed out of ${a.label}`);
    } catch (e) {
      toast.error(`Could not sign out of ${a.label}`, { description: (e as Error).message });
    } finally {
      setSigningOut(false);
    }
  };

  return (
    <article
      data-account={a.harness}
      aria-label={a.label}
      className={cn("flex min-w-0 flex-col rounded-xl border bg-card transition-colors duration-150", !a.installed && "bg-card/60")}
    >
      <header className="flex items-center gap-3 p-4 pb-3">
        <HarnessMark harness={a.harness} label={a.label} muted={!a.connected} />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h2 className="min-w-0 truncate text-[14px] font-semibold tracking-tight">{a.label}</h2>
            {a.connected && id.plan && (
              <span className="shrink-0 rounded-full bg-brand-soft px-2 text-2xs leading-[18px] font-medium text-brand">{id.plan}</span>
            )}
          </div>
          {a.connected ? (
            <p className="mt-0.5 truncate text-xs text-muted-foreground" title={[id.email, id.org].filter(Boolean).join(" · ")}>
              {[id.email || "Signed in", method, id.org].filter(Boolean).join(" · ")}
            </p>
          ) : (
            <p className="mt-0.5 flex items-center gap-1.5 text-xs text-muted-foreground">
              {login === "running" ? (
                <>
                  <span className="size-1.5 animate-pulse-dot rounded-full bg-state-working" aria-hidden />
                  Signing in…
                </>
              ) : a.installed ? (
                <>
                  <span className="size-1.5 rounded-full bg-state-idle" aria-hidden />
                  Not connected
                </>
              ) : (
                <>
                  <span className="size-1.5 rounded-full border border-state-idle" aria-hidden />
                  Not installed
                </>
              )}
            </p>
          )}
        </div>
      </header>

      {a.error && (
        <p className={cn("mx-4 mb-3 flex items-start gap-1.5 rounded-md bg-state-waiting/10 px-2.5 py-1.5 text-xs", AMBER)} role="status">
          <AlertTriangle className="mt-px size-3.5 shrink-0" aria-hidden />
          <span className="min-w-0 [overflow-wrap:anywhere]">{sentence(a.error)}</span>
        </p>
      )}

      <div className="flex flex-1 flex-col px-4 pb-4">
        {a.connected ? (
          limits.length > 0 ? (
            <div className="space-y-4 pt-1">
              {limits.map((l) => (
                <LimitRow key={l.id} limit={l} now={now} />
              ))}
            </div>
          ) : a.refreshing && !a.checked_at ? (
            <div className="space-y-3 pt-1" aria-busy="true">
              <Skeleton className="h-3 w-2/3" />
              <Skeleton className="h-2 w-full rounded-full" />
            </div>
          ) : (
            <p className="flex items-start gap-1.5 pt-1 text-xs leading-5 text-muted-foreground">
              <Info className="mt-[3px] size-3.5 shrink-0 text-subtle-foreground" aria-hidden />
              <span>{sentence(a.limits_note) || `${a.label} reported no quota.`}</span>
            </p>
          )
        ) : a.installed ? (
          <div className="flex flex-1 flex-col gap-3 pt-1">
            <p className="text-[13px] leading-5 text-muted-foreground">Sign in to see how much of {a.label}'s quota you have used.</p>
            {login === "failed" && (
              <p className="flex items-start gap-1.5 text-xs text-state-error" role="status">
                <AlertTriangle className="mt-px size-3.5 shrink-0" aria-hidden />
                <span className="min-w-0 [overflow-wrap:anywhere]">{sentence(a.login.message) || "The last sign-in did not finish."}</span>
              </p>
            )}
            <div className="mt-auto space-y-2 pt-3">
              <Button variant="brand" size="lg" className="w-full" disabled={starting} onClick={() => onConnect(a)}>
                {starting || login === "running" ? <Loader2 className="size-4 animate-spin" /> : <LogIn className="size-4" />}
                {login === "running" ? "Continue signing in" : login === "failed" ? "Try again" : `Connect ${a.label}`}
              </Button>
              <p className="text-center text-2xs text-subtle-foreground">Uses {a.label}'s own sign-in, in your browser.</p>
            </div>
          </div>
        ) : (
          <div className="flex flex-1 flex-col gap-3 pt-1">
            <p className="text-[13px] leading-5 text-muted-foreground">
              <span className="font-medium text-foreground">Install, then connect.</span> {a.label} is not on this machine yet.
            </p>
            {a.install && <InstallHint text={a.install} label={a.label} />}
            <Button variant="outline" size="sm" className="self-start" disabled={a.refreshing} onClick={() => refresh(a)}>
              <RefreshCw className={cn(a.refreshing && "animate-spin")} />
              {a.refreshing ? "Checking…" : "Check again"}
            </Button>
          </div>
        )}
      </div>

      {a.connected && (
        <footer className="flex items-center gap-1 border-t px-4 py-1.5">
          <span className="tabular min-w-0 flex-1 truncate text-2xs text-subtle-foreground">
            {a.refreshing ? "Checking…" : a.checked_at ? `Checked ${relativeTime(a.checked_at, now)}` : "Not checked yet"}
          </span>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="ghost" size="icon-xs" className="text-muted-foreground" aria-label={`Check ${a.label} again`} disabled={a.refreshing} onClick={() => refresh(a)}>
                <RefreshCw className={cn(a.refreshing && "animate-spin")} />
              </Button>
            </TooltipTrigger>
            <TooltipContent>Check again</TooltipContent>
          </Tooltip>
          <Button variant="ghost" size="xs" className="text-muted-foreground" disabled={signingOut} onClick={() => setAskOut(true)}>
            {signingOut && <Loader2 className="animate-spin" />}
            Sign out
          </Button>
        </footer>
      )}

      <AlertDialog open={askOut} onOpenChange={setAskOut}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Sign out of {a.label}?</AlertDialogTitle>
            <AlertDialogDescription>
              {a.label} on this machine signs out{id.email ? ` of ${id.email}` : ""}. Agents that use it stop working until you connect again.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Stay signed in</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={() => void signOut()}>
              Sign out
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </article>
  );
}

export function AccountCardSkeleton() {
  return (
    <div className="flex flex-col rounded-xl border bg-card p-4" aria-hidden>
      <div className="flex items-center gap-3">
        <Skeleton className="size-9 rounded-[9px]" />
        <div className="flex-1 space-y-1.5">
          <Skeleton className="h-3.5 w-24" />
          <Skeleton className="h-3 w-40" />
        </div>
      </div>
      <div className="mt-5 space-y-5">
        {[0, 1].map((i) => (
          <div key={i} className="space-y-2">
            <Skeleton className="h-3 w-1/2" />
            <Skeleton className="h-2 w-full rounded-full" />
          </div>
        ))}
      </div>
    </div>
  );
}
