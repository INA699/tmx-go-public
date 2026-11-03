// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

//go:build !windows

package hiddenexec

import "os/exec"

func configure(*exec.Cmd) {}

func configureInheritConsole(*exec.Cmd) {}

func configureGUI(*exec.Cmd) {}
