// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package fleet

import (
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

// SetRateLimitHook: fn learns at once when an agent's harness says a limit of the owner's plan
// stopped it (internal/accounts marks that account's limit as used up). It runs on a goroutine
// of its own, never under the fleet's lock. nil = nobody.
func (f *Fleet) SetRateLimitHook(fn func(h model.Harness, rl runner.RateLimit)) {
	f.mu.Lock()
	f.rateLimited = fn
	f.mu.Unlock()
}
