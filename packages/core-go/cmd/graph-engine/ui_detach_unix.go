//go:build !windows

package main

import "syscall"

// detachSysProcAttr detaches the background `serve` process spawned by `ui`
// from this process's session (setsid), so it keeps running independently
// of the `ui` command's own terminal/process group -- e.g. it must not be
// killed by a Ctrl-C sent to the shell that ran `graph-engine ui`.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
