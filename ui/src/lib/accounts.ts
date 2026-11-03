// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

// AI accounts (the Usage page, #/usage): which harness accounts are connected and how much of
// their quota is used, as the harnesses report it. GET /api/accounts on each connection, then
// accounts.updated events carry the whole payload. The sign-in runs the harness's own login
// in a terminal on the daemon (loginTerminalPath); its progress arrives as login.state.
import { create } from "zustand";
import { normalizeAccounts, type AccountsPayload, type AccountStatus } from "./accounts-format";
import { ownerAction, withConfirmation } from "./owner-gate";
import { onBusEvent, useStore } from "./store";
import type { BusEvent } from "./types";

export * from "./accounts-format";

interface AccountsState {
  payload: AccountsPayload | null;
  /** the first answer (or failure) is in */
  loaded: boolean;
  /** the last GET failed; the page shows it only when it has nothing else to show */
  error: { status: number; message: string } | null;
}

export const useAccounts = create<AccountsState>(() => ({ payload: null, loaded: false, error: null }));

function transport() {
  const t = useStore.getState().transport;
  if (!t) throw new Error("Not connected yet.");
  return t;
}

function put(p: Partial<AccountsPayload> | null | undefined): AccountsPayload | null {
  if (!p || !Array.isArray(p.accounts)) return useAccounts.getState().payload;
  const payload = normalizeAccounts(p);
  useAccounts.setState({ payload, loaded: true, error: null });
  return payload;
}

export async function loadAccounts(): Promise<void> {
  try {
    put(await transport().request<AccountsPayload>("GET", "/api/accounts"));
  } catch (e) {
    const err = e as { status?: number; message?: string };
    useAccounts.setState({ loaded: true, error: { status: err.status ?? 0, message: err.message || "The accounts could not be read." } });
  }
}

onBusEvent((ev: BusEvent) => {
  if (ev.type === "snapshot") void loadAccounts(); // on every (re)connection: the sidebar shows a full quota
  if (ev.type === "accounts.updated") put(ev.data as AccountsPayload);
});

const enc = encodeURIComponent;

export const accountsApi = {
  /** Asks the harnesses again (all of them without a harness); the answer says refreshing. */
  refresh: (harness?: string) =>
    transport()
      .request<AccountsPayload>("POST", "/api/accounts/refresh", harness ? { harness } : {})
      .then(put),
  /** Starts the harness's own sign-in on the daemon; its terminal is loginTerminalPath(harness). */
  login: (harness: string) =>
    ownerAction((c) => transport().request<{ session: string }>("POST", withConfirmation(`/api/accounts/${enc(harness)}/login`, c))),
  cancelLogin: (harness: string) => transport().request<unknown>("DELETE", `/api/accounts/${enc(harness)}/login`),
  logout: (harness: string) =>
    ownerAction((c) => transport().request<AccountsPayload>("POST", withConfirmation(`/api/accounts/${enc(harness)}/logout`, c))).then(put),
};

export function useAccount(harness: string): AccountStatus | undefined {
  return useAccounts((s) => s.payload?.accounts.find((a) => a.harness === harness));
}
