package terminal

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Screen lock states (TabDiagnostics.ScreenLock, DFLT-00362).
//
// While the screen is locked, macOS lets no app but loginwindow come to the
// front: Terminal's activate, System Events' `set frontmost` and `open -a
// Terminal` are all refused (launchservicesd: "isn't in fPermittedFrontApps
// (loginwindow), so denying"). The tab script then either waits for
// Terminal in vain (9103) or, when Terminal was frontmost when the screen
// locked, sees it still "frontmost" and sends Cmd+T -- which goes to the
// lock screen's password field instead (9102). So the tab is not tried at
// all while the screen is locked (TabFailureScreenLocked).
const (
	// ScreenLocked: the session's CGSSessionScreenIsLocked is set.
	ScreenLocked = "locked"
	// ScreenUnlocked: it is not set, or the session is not found.
	ScreenUnlocked = "unlocked"
	// ScreenLockUnknown: the state could not be read (ioreg failed or
	// answered something unexpected). Treated like ScreenUnlocked: the tab
	// is tried as before.
	ScreenLockUnknown = "unknown"
)

// screenLockTimeout bounds the ioreg run of readScreenLockState.
const screenLockTimeout = 2 * time.Second

// screenLockState reads whether the screen is locked. A package variable so
// tests can fake it without running ioreg.
var screenLockState = readScreenLockState

// readScreenLockState asks ioreg for the console sessions (read only: it
// moves no window and needs no permission) and reports this user's lock
// state. Any failure is ScreenLockUnknown, so a misread never stops a tab
// that could have opened: a wrong "locked" would send a session to a new
// window for nothing, while a wrong "unlocked" only does what every launch
// did before DFLT-00362.
func readScreenLockState() string {
	ctx, cancel := context.WithTimeout(context.Background(), screenLockTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ioreg", "-n", "Root", "-d1", "-a").Output()
	if err != nil {
		return ScreenLockUnknown
	}
	return screenLockFromIORegPlist(out, os.Getuid())
}

// screenLockFromIORegPlist reads the lock state of uid's console session
// from `ioreg -n Root -d1 -a` (an XML property list whose root dictionary
// has IOConsoleUsers, one dictionary per login session). A session is taken
// as locked only when its CGSSessionScreenIsLocked is true (or 1): the key
// is absent while the screen is unlocked. A session of another user is
// skipped (fast user switching); one without a user ID is taken as ours.
func screenLockFromIORegPlist(data []byte, uid int) string {
	root, err := decodePlist(data)
	if err != nil {
		return ScreenLockUnknown
	}
	dict, ok := root.(map[string]any)
	if !ok {
		return ScreenLockUnknown
	}
	users, ok := dict["IOConsoleUsers"].([]any)
	if !ok {
		return ScreenLockUnknown
	}
	for _, u := range users {
		session, ok := u.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := session["kCGSSessionUserIDKey"].(int64); ok && id != int64(uid) {
			continue
		}
		switch v := session["CGSSessionScreenIsLocked"].(type) {
		case bool:
			if v {
				return ScreenLocked
			}
		case int64:
			if v != 0 {
				return ScreenLocked
			}
		}
	}
	return ScreenUnlocked
}

// decodePlist decodes an XML property list into Go values: a dict is a
// map[string]any, an array a []any, an integer an int64 (a string when it
// does not parse), true and false a bool, and every other element (string,
// real, date, data) its text.
func decodePlist(data []byte) (any, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := d.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, errors.New("plist: no value")
			}
			return nil, err
		}
		if se, ok := tok.(xml.StartElement); ok {
			if se.Name.Local == "plist" {
				continue
			}
			return decodePlistValue(d, se, 0)
		}
	}
}

// maxPlistDepth bounds the nesting decodePlistValue follows.
const maxPlistDepth = 32

// decodePlistValue decodes the value se starts, depth levels down.
func decodePlistValue(d *xml.Decoder, se xml.StartElement, depth int) (any, error) {
	if depth > maxPlistDepth {
		return nil, errors.New("plist: nested too deeply")
	}
	switch se.Name.Local {
	case "dict":
		return decodePlistDict(d, depth)
	case "array":
		return decodePlistArray(d, depth)
	case "true", "false":
		if err := d.Skip(); err != nil {
			return nil, err
		}
		return se.Name.Local == "true", nil
	}
	var text string
	if err := d.DecodeElement(&text, &se); err != nil {
		return nil, err
	}
	if se.Name.Local == "integer" {
		if n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64); err == nil {
			return n, nil
		}
	}
	return text, nil
}

// decodePlistDict decodes the members of a dict whose start element was
// just read, up to its end element: each <key> names the value after it (a
// value with no key before it is skipped).
func decodePlistDict(d *xml.Decoder, depth int) (map[string]any, error) {
	m := map[string]any{}
	key, haveKey := "", false
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "key" {
				if err := d.DecodeElement(&key, &t); err != nil {
					return nil, err
				}
				haveKey = true
				continue
			}
			v, err := decodePlistValue(d, t, depth+1)
			if err != nil {
				return nil, err
			}
			if haveKey {
				m[key] = v
				haveKey = false
			}
		case xml.EndElement:
			return m, nil
		}
	}
}

// decodePlistArray decodes the items of an array whose start element was
// just read, up to its end element. An empty array is an empty, non-nil
// slice.
func decodePlistArray(d *xml.Decoder, depth int) ([]any, error) {
	list := []any{}
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			v, err := decodePlistValue(d, t, depth+1)
			if err != nil {
				return nil, err
			}
			list = append(list, v)
		case xml.EndElement:
			return list, nil
		}
	}
}
