// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package config

// ProductName isolates the fleet binary, service and MCP namespace from Python tmx.
const ProductName = "tmxg"

// On reports whether Companion was explicitly enabled. Trial loading always disables it.
func (c *CompanionConfig) On() bool { return c != nil && c.Enabled != nil && *c.Enabled }
