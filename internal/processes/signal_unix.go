// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

//go:build !windows

package processes

import (
	"os"
	"syscall"
)

func graceful(p *os.Process) { _ = p.Signal(syscall.SIGTERM) }
