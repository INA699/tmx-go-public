// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package runner

import "golang.org/x/sys/windows"

func processElevated() bool { return windows.GetCurrentProcessToken().IsElevated() }
