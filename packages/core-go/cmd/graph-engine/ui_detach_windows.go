//go:build windows

package main

import "syscall"

// detachSysProcAttr is a best-effort no-op on Windows: unlike internal/
// terminal's OS branches (also macOS-centric, see its doc comment), proper
// process-group detachment on Windows is left as a future improvement
// rather than guessed at here -- see the execution plan's noted Windows
// limitation (art-630d82eb section 7).
func detachSysProcAttr() *syscall.SysProcAttr {
	return nil
}
