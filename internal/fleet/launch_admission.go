// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package fleet

import (
	"runtime"

	"github.com/blackcode-ch/tmx-go/internal/agentpolicy"
	"github.com/blackcode-ch/tmx-go/internal/harness"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// Overridden only by fake-runner tests of platform-independent fleet behavior.
var launchGOOS = runtime.GOOS

// launchAdmissionLocked is shared by creation, adoption, restart, resume, and
// the final spawn path. Keep it free of filesystem/store/process mutations.
func (f *Fleet) launchAdmissionLocked(a model.Agent) error {
	// Shell registration and control are owner-only. It is the owner's terminal,
	// not an agent, and must remain usable to recover from agent policy failures.
	if a.Harness == model.HarnessShell {
		return nil
	}
	if f.opts.Elevated() {
		return errf(KindForbidden, "refusing to launch from an elevated process")
	}
	if err := harness.ValidateLaunchCwd(a.Cwd); err != nil {
		return errf(KindForbidden, "%s", err)
	}
	if err := agentpolicy.CheckLaunch(agentpolicy.LaunchPolicy{GOOS: launchGOOS, Harness: string(a.Harness), CodexParityGuard: f.codexParityEligibleLocked(a)}); err != nil {
		return errf(KindForbidden, "%s", err)
	}
	return f.guardAdmissionLocked(a)
}

func (f *Fleet) guardAdmissionLocked(a model.Agent) error {
	if a.Harness == model.HarnessShell {
		return nil
	}
	if !f.safetyLocked().settings.On(string(a.Level), a.Profile) {
		if launchGOOS == "windows" {
			return errf(KindForbidden, "Windows launches require an enabled command guard")
		}
		return nil
	}
	if a.Harness != model.HarnessClaude {
		if f.codexParityEligibleLocked(a) {
			return nil
		}
		return errf(KindForbidden, "the command guard has no enforced adapter for %s", a.Harness)
	}
	if where := hooksDisabledIn(a.Cwd); where != "" {
		return errf(KindForbidden, "the command guard is disabled in %s", where)
	}
	return nil
}

func (f *Fleet) codexParityEligibleLocked(a model.Agent) bool {
	return launchGOOS == "windows" && a.Harness == model.HarnessCodex && !a.CodexFullAccess && !f.profileForLocked(a).ReadOnly && f.opts.Executable != ""
}
