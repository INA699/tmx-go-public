//go:build linux

package sysinfo

import "strconv"

// processStopped: the process is stopped (SIGSTOP), from /proc/<pid>/stat.
func processStopped(pid int) bool {
	f, ok := statFields(strconv.Itoa(pid))
	return ok && len(f) > 0 && f[0] == "T"
}
