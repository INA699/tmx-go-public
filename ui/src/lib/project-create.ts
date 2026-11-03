// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

import type { CreateProjectBody, Harness, HarnessInfo } from "./types";

export function projectLeadHarnesses(harnesses: HarnessInfo[]): HarnessInfo[] {
  return harnesses.filter((h) => h.modes.includes("chat"));
}

export function createProjectPayload(input: {
  name: string;
  harness: Harness;
  folder?: string;
  brief?: string;
}): CreateProjectBody {
  return {
    name: input.name,
    harness: input.harness,
    ...(input.folder ? { folder: input.folder } : {}),
    ...(input.brief ? { brief: input.brief } : {}),
  };
}
