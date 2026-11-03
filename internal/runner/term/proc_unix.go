// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

//go:build !windows

package term

import (
	"syscall"

	pty "github.com/aymanbagabas/go-pty"
)

// prepareCommand changes nothing outside Windows: executables start as they are.
func prepareCommand(*pty.Cmd) {}

// afterStart drops the parent's copy of the tty, so reads on the master end with EIO once
// every program in the terminal has exited, after all output was delivered.
func afterStart(pt pty.Pty) {
	if u, ok := pt.(pty.UnixPty); ok {
		_ = u.Slave().Close()
	}
}

// go-pty starts the program as a session leader (Setsid), so its pid is the group to signal.
func signalGroup(cmd *pty.Cmd, sigs ...syscall.Signal) {
	if cmd.Process == nil || cmd.Process.Pid <= 0 {
		return
	}
	for _, s := range sigs {
		_ = syscall.Kill(-cmd.Process.Pid, s)
	}
}

// terminate: interactive shells ignore SIGTERM but not SIGHUP.
func terminate(cmd *pty.Cmd) { signalGroup(cmd, syscall.SIGHUP, syscall.SIGTERM) }
func kill(cmd *pty.Cmd)      { signalGroup(cmd, syscall.SIGKILL) }
