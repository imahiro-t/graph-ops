package terminal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/graph-ops/core-go/internal/displayname"
)

// The input state around a failed tab launch (DFLT-00377): whether a
// process held secure keyboard entry, and how long ago the user last typed
// or moved the mouse. Both are read only after a tab failed, outside the
// tab lock, so a tab that opens never waits for them; both only read the
// I/O registry (no window moves, no permission needed), are bounded to
// inputStateTimeout, and give "" when they cannot be read.

// inputStateTimeout bounds each read of secureInputOwner and hidIdleMS
// (each normally takes about 15 milliseconds).
const inputStateTimeout = 1 * time.Second

// secureInputOwner and hidIdleMS are package variables so tests can fake
// them without running ioreg.
var (
	secureInputOwner = readSecureInputOwner
	hidIdleMS        = readHIDIdleMS
)

// readSecureInputOwner returns the process holding secure keyboard entry in
// this user's console session ("<pid> <name>", or "<pid>" when ps cannot
// name it), "" when none does or it cannot be read.
func readSecureInputOwner() string {
	ctx, cancel := context.WithTimeout(context.Background(), inputStateTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ioreg", "-n", "Root", "-d1", "-a").Output()
	if err != nil {
		return ""
	}
	pid := secureInputPIDFromIORegPlist(out, os.Getuid())
	if pid <= 0 {
		return ""
	}
	name, err := exec.CommandContext(ctx, "ps", "-o", "comm=", "-p", strconv.FormatInt(pid, 10)).Output()
	if err != nil {
		return strconv.FormatInt(pid, 10)
	}
	return displayname.Sanitize(strconv.FormatInt(pid, 10) + " " + filepath.Base(strings.TrimSpace(string(name))))
}

// secureInputPIDFromIORegPlist reads kCGSSessionSecureInputPID of uid's
// console session from `ioreg -n Root -d1 -a` (see screenLockFromIORegPlist
// for the format; the key is absent while no process holds secure input).
// It returns 0 when there is none or the data cannot be read.
func secureInputPIDFromIORegPlist(data []byte, uid int) int64 {
	root, err := decodePlist(data)
	if err != nil {
		return 0
	}
	dict, ok := root.(map[string]any)
	if !ok {
		return 0
	}
	users, ok := dict["IOConsoleUsers"].([]any)
	if !ok {
		return 0
	}
	for _, u := range users {
		session, ok := u.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := session["kCGSSessionUserIDKey"].(int64); ok && id != int64(uid) {
			continue
		}
		if pid, ok := session["kCGSSessionSecureInputPID"].(int64); ok && pid > 0 {
			return pid
		}
	}
	return 0
}

// hidIdlePattern matches HIDIdleTime (nanoseconds) in `ioreg -c IOHIDSystem`.
var hidIdlePattern = regexp.MustCompile(`"HIDIdleTime"\s*=\s*([0-9]+)`)

// readHIDIdleMS returns the milliseconds since the last keyboard or mouse
// input, "" when it cannot be read.
func readHIDIdleMS() string {
	ctx, cancel := context.WithTimeout(context.Background(), inputStateTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ioreg", "-c", "IOHIDSystem", "-r", "-d", "1", "-k", "HIDIdleTime").Output()
	if err != nil {
		return ""
	}
	return hidIdleMSFromIOReg(string(out))
}

// hidIdleMSFromIOReg reads the first HIDIdleTime in ioreg's output as
// milliseconds, "" when there is none.
func hidIdleMSFromIOReg(out string) string {
	m := hidIdlePattern.FindStringSubmatch(out)
	if m == nil {
		return ""
	}
	ns, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		return ""
	}
	return strconv.FormatUint(ns/uint64(time.Millisecond), 10)
}
