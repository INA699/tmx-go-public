// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

//go:build windows

package term

import (
	"github.com/blackcode-ch/tmx-go/internal/sysinfo"
	"os"
	"path/filepath"
	"strings"

	pty "github.com/aymanbagabas/go-pty"
	"github.com/blackcode-ch/tmx-go/internal/hiddenexec"
)

// prepareCommand makes a batch launcher runnable in a ConPTY. CreateProcess cannot execute
// .cmd/.bat files, and npm installs its CLIs (opencode, claude, codex, ...) as .cmd shims on
// Windows, so those run through cmd.exe. The command line is built by hand because cmd's /c
// quoting rules are not CommandLineToArgvW's (ComposeCommandLine would escape it wrongly).
func prepareCommand(cmd *pty.Cmd) {
	cmd.SysProcAttr = hiddenexec.ConPTYAttributes(cmd.SysProcAttr)
	ext := strings.ToLower(filepath.Ext(cmd.Path))
	if ext != ".cmd" && ext != ".bat" {
		return
	}
	comspec := os.Getenv("COMSPEC")
	if comspec == "" {
		if root := os.Getenv("SystemRoot"); root != "" {
			comspec = filepath.Join(root, "System32", "cmd.exe")
		} else {
			comspec = "cmd.exe"
		}
	}
	var b strings.Builder
	b.WriteString(quoteCmd(cmd.Path))
	for _, a := range cmd.Args[1:] {
		b.WriteString(" ")
		b.WriteString(quoteCmd(a))
	}
	// /d: no AutoRun; /s: strip exactly the outer pair of quotes, keeping the shim path
	// quoted; /c: run it and exit. The whole command line is given raw to CreateProcess.
	cmd.SysProcAttr.CmdLine = quoteCmd(comspec) + ` /d /s /c "` + b.String() + `"`
	cmd.Path = comspec
	cmd.Args = []string{comspec}
}

// quoteCmd quotes one argument for cmd.exe. Batch shims only receive paths, flags, model
// names and ids here; an embedded double quote is doubled, which cmd reads literally.
func quoteCmd(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"&|<>^()%!,;=") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func afterStart(pty.Pty) {}

// Windows has no signals for console programs; both steps end the tree by PID.
func terminate(cmd *pty.Cmd) { killTree(cmd) }
func kill(cmd *pty.Cmd)      { killTree(cmd) }

func killTree(cmd *pty.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = sysinfo.KillStarted(cmd.Process)
}
