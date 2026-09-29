// Package identity answers "who is this" for the features that several
// members sharing one data source need (DFLT-00325's review, section 3-1):
// the autopilot's shared runs (DFLT-00326), and after it the node claims,
// decision records and concurrent-edit checks of DFLT-00327..00330.
//
// It provides three things:
//
//   - a display name (Actor.Name): the home config's myName, or
//     "<OS user>@<host>" when that is empty (Actor.NameIsFallback),
//     sanitized and capped by displayname.Sanitize (DFLT-00336) since other
//     members' machines print it;
//   - a machine ID (Actor.MachineID): a UUID generated once per home
//     directory and kept in $HOME/.graph-ops/machine-id;
//   - session IDs (NewSessionID): a fresh random ID per acquisition or
//     start.
//
// The name is for people to read and is never used to match anything:
// two members may well share a name (or both leave it empty on hosts with
// the same name). Matching is done by the machine ID and a session (or run)
// ID only.
package identity

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/graph-ops/core-go/internal/displayname"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
)

// Actor is who is acting in this process.
type Actor struct {
	// Name is the display name: the home config's myName, or
	// "<OS user>@<host>" (a part that cannot be read, or is empty once
	// sanitized, is "unknown") when that is empty once sanitized. Always
	// passed through displayname.Sanitize -- no control or invisible format
	// characters, at most displayname.MaxRunes runes. Never "". For display
	// only -- never compare it.
	Name string `json:"name"`
	// NameIsFallback is true when Name was made from the OS user and host
	// because myName is not set, so a display can say "(name not set)".
	NameIsFallback bool `json:"name_is_fallback"`
	// MachineID identifies the home directory this process runs under (see
	// MachineID). It is what "the same machine" means.
	MachineID string `json:"machine_id"`
}

// MachineIDFile is the file, relative to the home directory, that keeps
// the machine ID.
const MachineIDFile = ".graph-ops/machine-id"

// unknownPart replaces an OS user or host name that cannot be read.
const unknownPart = "unknown"

// Seams for tests: the OS user name and host name.
var (
	currentUsername = func() (string, error) {
		u, err := user.Current()
		if err != nil {
			return "", err
		}
		return u.Username, nil
	}
	hostname = os.Hostname
)

// Resolve returns the Actor of homeDir: its display name and its machine ID
// (created on first use). It fails only when the machine ID cannot be read
// or created -- a broken machine-id file included, which is never silently
// replaced: a new ID would make this machine a stranger to its own
// interrupted autopilot runs.
func Resolve(homeDir string) (Actor, error) {
	name, fallback := DisplayName(homeDir)
	id, err := MachineID(homeDir)
	if err != nil {
		return Actor{}, err
	}
	return Actor{Name: name, NameIsFallback: fallback, MachineID: id}, nil
}

// DisplayName returns homeDir's display name and whether it is the
// "<OS user>@<host>" fallback. An unreadable home config counts as one with
// no myName.
func DisplayName(homeDir string) (name string, fallback bool) {
	cfg, _ := runtimeconfig.LoadHomeConfig(homeDir)
	if n := displayname.Sanitize(cfg.MyName); n != "" {
		return n, false
	}
	u, err := currentUsername()
	if err != nil {
		u = ""
	}
	// A Windows user name may carry a DOMAIN\ prefix; keep only the name.
	u = strings.TrimSpace(u)
	if i := strings.LastIndex(u, `\`); i >= 0 && i < len(u)-1 {
		u = u[i+1:]
	}
	u = fallbackPart(u)
	h, err := hostname()
	if err != nil {
		h = ""
	}
	h = fallbackPart(h)
	// Each part is capped already; this caps the two together.
	return displayname.Sanitize(u + "@" + h), true
}

// fallbackPart sanitizes one part of the "<OS user>@<host>" fallback name,
// replacing one that is empty (or becomes empty) with "unknown".
func fallbackPart(s string) string {
	if s = displayname.Sanitize(s); s == "" {
		return unknownPart
	}
	return s
}

// MachineID returns homeDir's machine ID, generating and saving it on first
// use. Several processes generating it at the same moment all end up with
// the one that was saved first: each writes its candidate to a temporary
// file and hard-links it into place, which only one link can win, and then
// every process reads back what is there.
func MachineID(homeDir string) (string, error) {
	if strings.TrimSpace(homeDir) == "" {
		return "", errors.New("machine id: the home directory could not be resolved")
	}
	path := filepath.Join(homeDir, filepath.FromSlash(MachineIDFile))
	if id, err := readMachineID(path); err == nil {
		return id, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("machine id: creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".machine-id-*")
	if err != nil {
		return "", fmt.Errorf("machine id: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	_, werr := tmp.WriteString(uuid.NewString() + "\n")
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		if werr == nil {
			werr = cerr
		}
		return "", fmt.Errorf("machine id: writing %s: %w", tmpPath, werr)
	}
	if err := os.Link(tmpPath, path); err != nil && !errors.Is(err, os.ErrExist) {
		// No hard links here (some file systems): fall back to an
		// exclusive create, which also lets only the first writer win.
		if f, cerr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); cerr == nil {
			data, _ := os.ReadFile(tmpPath)
			_, werr := f.Write(data)
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			if werr != nil {
				return "", fmt.Errorf("machine id: writing %s: %w", path, werr)
			}
		} else if !errors.Is(cerr, os.ErrExist) {
			return "", fmt.Errorf("machine id: saving %s: %w", path, err)
		}
	}
	return readMachineID(path)
}

// readMachineID reads and validates the machine-id file. A missing file is
// os.ErrNotExist; anything that is not one canonical UUID is an error.
func readMachineID(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		return "", fmt.Errorf("machine id: reading %s: %w", path, err)
	}
	id := strings.TrimSpace(string(data))
	if !validUUID(id) {
		return "", fmt.Errorf("machine id: %s does not hold a UUID; it is left as is -- restore it from a backup, or remove it to get a new ID (this machine then can no longer resume its interrupted autopilot runs)", path)
	}
	return id, nil
}

func validUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	u, err := uuid.Parse(s)
	return err == nil && u.String() == strings.ToLower(s)
}

// ValidSessionID reports whether s has the shape NewSessionID gives (a
// lowercase UUID). graph-engine checks every session ID and claim token a
// caller passes with it before storing or comparing it.
func ValidSessionID(s string) bool { return validUUID(s) }

// NewSessionID returns a fresh random session ID (a UUID v4), for one
// acquisition or start. Two calls never return the same value in practice.
func NewSessionID() string { return uuid.NewString() }
