// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

//go:build !windows

package runner

import "os"

func processElevated() bool { return os.Geteuid() == 0 }
