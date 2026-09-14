// Package browser opens a URL in the operating system's default web
// browser. It is deliberately separate from internal/terminal, which solves
// a related but different problem -- launching an interactive terminal
// emulator with a shell command to run `claude` in -- rather than being
// folded into it or reused as-is; see the execution plan for GOPS-00001
// (art-630d82eb, section 4.1 step 5) for the reasoning.
package browser

import (
	"fmt"
	"os/exec"
	"runtime"
)

// goos is a package variable (not a direct runtime.GOOS reference) so tests
// can fake a different platform, same pattern as internal/terminal.goos.
var goos = runtime.GOOS

// Open launches url in the OS's default browser. It does not wait for the
// browser process itself to exit (nor does it know how to check whether the
// URL actually loaded) -- only a failure to even spawn the OS-level opener
// is reported.
func Open(url string) error {
	name, args, err := buildOpenArgv(url)
	if err != nil {
		return err
	}
	return exec.Command(name, args...).Start()
}

// buildOpenArgv picks the OS-appropriate way to open a URL. Kept separate
// from Open so the selection logic is unit-testable without actually
// spawning a process.
func buildOpenArgv(url string) (string, []string, error) {
	switch goos {
	case "darwin":
		return "open", []string{url}, nil
	case "windows":
		// rundll32's url.dll,FileProtocolHandler is the standard no-shell way
		// to open a URL in the default browser on Windows.
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}, nil
	case "linux":
		return "xdg-open", []string{url}, nil
	default:
		return "", nil, fmt.Errorf("opening a browser is not supported on %s", goos)
	}
}
