// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

//go:build !darwin

package sysinfo

func wrapLow(path string, args []string) (string, []string) { return path, args }
