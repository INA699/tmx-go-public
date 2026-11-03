// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

// Usage (#/usage): the AI accounts this machine's harnesses are signed in to (Claude, Codex,
// Antigravity) and how much of each plan's quota is used, as the harnesses report it. Connect
// is one click: it runs the harness's own sign-in and shows its terminal in a dialog.
import { useCallback, useEffect, useState } from "react";
import { AlertTriangle, Gauge, PlugZap, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { AccountCard, AccountCardSkeleton } from "@/components/accounts/account-card";
import { ConnectDialog, type Connecting } from "@/components/accounts/connect-dialog";
import { TONE_TEXT } from "@/components/accounts/usage-bits";
import { EmptyState, PageBody, PageHeader, PageTitle } from "@/components/page";
import { useNow } from "@/hooks/use-now";
import {
  accountsApi,
  connectedSummary,
  fullestLimit,
  lastChecked,
  limitTone,
  listWords,
  loadAccounts,
  resetLabel,
  sentence,
  useAccounts,
  usedPct,
  type AccountStatus,
} from "@/lib/accounts";
import { relativeTime } from "@/lib/format";
import { cn } from "@/lib/utils";

/** Older than this when the page opens: ask the harnesses again. */
const STALE_MS = 5 * 60_000;

export function UsagePage() {
  const payload = useAccounts((s) => s.payload);
  const loaded = useAccounts((s) => s.loaded);
  const error = useAccounts((s) => s.error);
  const now = useNow(30_000);
  const [asking, setAsking] = useState(false);
  const [starting, setStarting] = useState<string | null>(null);
  const [connecting, setConnecting] = useState<Connecting | null>(null);

  useEffect(() => {
    void loadAccounts().then(() => {
      const p = useAccounts.getState().payload;
      const installed = p?.accounts.filter((a) => a.installed) ?? [];
      if (installed.length && !installed.some((a) => a.refreshing) && Date.now() - lastChecked(installed) > STALE_MS) {
        void accountsApi.refresh().catch(() => {});
      }
    });
  }, []);

  const refreshAll = async () => {
    setAsking(true);
    try {
      await accountsApi.refresh();
    } catch (e) {
      toast.error("Could not check the accounts", { description: (e as Error).message });
    } finally {
      setAsking(false);
    }
  };

  const connect = useCallback(async (a: AccountStatus) => {
    if (a.login.state === "running") {
      setConnecting({ harness: a.harness, session: `running-${a.harness}` }); // one already runs: show it
      return;
    }
    setStarting(a.harness);
    try {
      const r = await accountsApi.login(a.harness);
      setConnecting({ harness: a.harness, session: r?.session || `${a.harness}-${Date.now()}` });
    } catch (e) {
      const err = e as Error & { status?: number };
      if (err.status === 409) {
        toast.error(`${a.label} is not installed on this machine`, { description: err.message });
        void loadAccounts();
      } else {
        toast.error(`Could not start ${a.label}'s sign-in`, { description: err.message });
      }
    } finally {
      setStarting(null);
    }
  }, []);
  const closeDialog = useCallback(() => setConnecting(null), []);

  const accounts = payload?.accounts ?? [];
  const refreshing = asking || accounts.some((a) => a.refreshing);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader
        actions={
          <Button variant="outline" size="sm" disabled={!payload || refreshing} onClick={() => void refreshAll()}>
            <RefreshCw className={cn(refreshing && "animate-spin")} />
            {refreshing ? "Checking…" : "Refresh"}
          </Button>
        }
      >
        <Gauge className="size-4 shrink-0 text-muted-foreground" />
        <PageTitle>Usage</PageTitle>
      </PageHeader>
      <PageBody width="max-w-[1120px]" className="space-y-6">
        {!payload ? (
          loaded && error ? (
            <EmptyState
              icon={PlugZap}
              title={error.status === 404 ? "This tmx cannot read AI accounts yet" : "Could not load your AI accounts"}
              actions={
                <Button variant="outline" size="sm" onClick={() => void loadAccounts()}>
                  <RefreshCw />
                  Try again
                </Button>
              }
            >
              {error.status === 404 ? "Update tmx to see your accounts and how much of their quota is used." : sentence(error.message)}
            </EmptyState>
          ) : (
            <>
              <div className="space-y-2" aria-busy="true" aria-label="Loading your AI accounts">
                <Skeleton className="h-6 w-72 max-w-full" />
                <Skeleton className="h-4 w-96 max-w-full" />
              </div>
              <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
                <AccountCardSkeleton />
                <AccountCardSkeleton />
                <AccountCardSkeleton />
              </div>
            </>
          )
        ) : accounts.length === 0 ? (
          <EmptyState icon={Gauge} title="No AI tools to show">
            This tmx knows no harness with an account to connect.
          </EmptyState>
        ) : (
          <>
            <Summary accounts={accounts} now={now} />
            <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
              {accounts.map((a) => (
                <AccountCard key={a.harness} a={a} now={now} starting={starting === a.harness} onConnect={(x) => void connect(x)} />
              ))}
            </div>
          </>
        )}
      </PageBody>
      <ConnectDialog connecting={connecting} onClose={closeDialog} onRetry={(a) => void connect(a)} />
    </div>
  );
}

function Summary({ accounts, now }: { accounts: AccountStatus[]; now: number }) {
  const checked = lastChecked(accounts.filter((a) => a.connected));
  const full = fullestLimit(accounts);
  const p = full ? usedPct(full.limit.used_percent) : 0;
  const tone = limitTone(p);
  const installed = accounts.filter((a) => a.installed);
  return (
    <section className="flex flex-wrap items-end gap-x-6 gap-y-3" aria-label="Summary">
      <div className="min-w-0 flex-1 basis-72">
        <h2 className="text-xl font-semibold tracking-tight text-balance">{connectedSummary(accounts)}</h2>
        <p className="mt-1 text-[13px] leading-5 text-muted-foreground">
          {installed.length === 0
            ? `Install ${listWords(accounts.map((a) => a.label), "or")}, then connect it here to see how much of its quota you have used.`
            : `How much of each plan's quota you have used, as ${listWords(installed.map((a) => a.label))} report${installed.length === 1 ? "s" : ""} it.`}
          {checked > 0 && <span className="tabular text-subtle-foreground"> Checked {relativeTime(checked, now)}.</span>}
        </p>
      </div>
      {full && tone !== "ok" && (
        <div
          className={cn(
            "flex max-w-full min-w-0 items-center gap-2.5 rounded-lg border px-3 py-2 text-xs",
            tone === "hot" ? "border-state-error/30 bg-state-error/[0.06]" : "border-state-waiting/40 bg-state-waiting/[0.08]",
          )}
          role="status"
        >
          <AlertTriangle className={cn("size-3.5 shrink-0", TONE_TEXT[tone])} aria-hidden />
          <span className="min-w-0 truncate">
            <span className="font-medium">{full.account.label}</span>
            <span className="text-muted-foreground"> · {full.limit.label} </span>
            <span className={cn("tabular font-semibold", TONE_TEXT[tone])}>{p}% used</span>
          </span>
          {resetLabel(full.limit, now) && <span className="tabular hidden shrink-0 text-muted-foreground sm:inline">{resetLabel(full.limit, now).replace(/^Resets/, "resets")}</span>}
        </div>
      )}
    </section>
  );
}
