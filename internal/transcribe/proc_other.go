// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

//go:build !windows

package transcribe

import "os/exec"

func hideWindow(*exec.Cmd) {}
