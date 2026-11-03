// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

// Fleet-wide Overview aggregation (WP4, internal/api/fleethosts.go): other local daemons
// configured in config.json "hosts", read over loopback only - no p2p, no new network
// exposure (ARCHITECTURE.md plan phase 10, "multi-machine", is not built; issue #90 keeps p2p off).
// Line-building and status logic is pure, in fleet-hosts-format.ts; this file is the zustand
// store and the fetch, kept apart so the pure logic stays unit-testable without a connection.
import { create } from "zustand";
import { fleetHostsResult, hidesOnStatus } from "./fleet-hosts-format";
import { useStore } from "./store";

export interface HostSummary {
  name: string;
  port: number;
  reachable: boolean;
  error?: string;
  machine?: string;
  agents: number;
  projects: number;
  running: number;
  paused: number;
  needs_you: number;
  cpu?: number;
  mem_free_mb?: number;
  disk_free_gb?: number;
  head_name?: string;
  head_state?: string;
  head_line?: string;
}

interface FleetHostsState {
  loaded: boolean;
  hosts: HostSummary[];
}

export const useFleetHosts = create<FleetHostsState>(() => ({ loaded: false, hosts: [] }));

function transport() {
  const t = useStore.getState().transport;
  if (!t) throw new Error("Not connected yet.");
  return t;
}

/** Fetches the current merge. The owner only (an agent gets 403); an older daemon, 404/501. */
export async function loadFleetHosts() {
  try {
    const v = await transport().request<{ hosts: HostSummary[] | null }>("GET", "/api/fleet/hosts");
    useFleetHosts.setState(fleetHostsResult(v.hosts));
  } catch (e) {
    const status = (e as { status?: number }).status;
    if (hidesOnStatus(status)) useFleetHosts.setState({ loaded: true, hosts: [] });
  }
}
