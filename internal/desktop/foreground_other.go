// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

//go:build desktop && !windows

package desktop

// allowForeground: only Windows restricts which process may bring a window to the front.
func allowForeground() {}
