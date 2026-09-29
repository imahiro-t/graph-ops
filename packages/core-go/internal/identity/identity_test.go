package identity

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/graph-ops/core-go/internal/displayname"
)

func writeHomeConfig(t *testing.T, home, myName string) {
	t.Helper()
	dir := filepath.Join(home, ".graph-ops")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"myName": ` + quote(myName) + `}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func quote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func stubOS(t *testing.T, user string, userErr error, host string, hostErr error) {
	t.Helper()
	oldU, oldH := currentUsername, hostname
	currentUsername = func() (string, error) { return user, userErr }
	hostname = func() (string, error) { return host, hostErr }
	t.Cleanup(func() { currentUsername, hostname = oldU, oldH })
}

func TestResolveUsesTrimmedMyName(t *testing.T) {
	home := t.TempDir()
	writeHomeConfig(t, home, "  Alice  ")
	a, err := Resolve(home)
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "Alice" || a.NameIsFallback {
		t.Fatalf("got %+v, want Alice (not a fallback)", a)
	}
}

func TestResolveFallsBackToUserAtHost(t *testing.T) {
	for _, myName := range []string{"", "   　  "} {
		home := t.TempDir()
		writeHomeConfig(t, home, myName)
		stubOS(t, "taro", nil, "mac01", nil)
		a, err := Resolve(home)
		if err != nil {
			t.Fatal(err)
		}
		if a.Name != "taro@mac01" || !a.NameIsFallback {
			t.Fatalf("myName %q: got %+v, want taro@mac01 (fallback)", myName, a)
		}
	}
}

func TestResolveFillsUnreadablePartsWithUnknown(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		user    string
		userErr error
		host    string
		hostErr error
		want    string
	}{
		{"taro", nil, "", boom, "taro@unknown"},
		{"", boom, "mac01", nil, "unknown@mac01"},
		{"", boom, "", boom, "unknown@unknown"},
	}
	for _, c := range cases {
		home := t.TempDir() // no config.json at all: no myName
		stubOS(t, c.user, c.userErr, c.host, c.hostErr)
		a, err := Resolve(home)
		if err != nil {
			t.Fatal(err)
		}
		if a.Name != c.want || !a.NameIsFallback {
			t.Fatalf("got %+v, want %s (fallback)", a, c.want)
		}
	}
}

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestMachineIDIsCreatedOnceAndReused(t *testing.T) {
	home := t.TempDir()
	a1, err := Resolve(home)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := Resolve(home)
	if err != nil {
		t.Fatal(err)
	}
	if !uuidV4.MatchString(a1.MachineID) || a1.MachineID != a2.MachineID {
		t.Fatalf("machine ids %q / %q", a1.MachineID, a2.MachineID)
	}
	data, err := os.ReadFile(filepath.Join(home, ".graph-ops", "machine-id"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != a1.MachineID+"\n" {
		t.Fatalf("file holds %q", got)
	}
	// No temp files are left behind.
	entries, _ := os.ReadDir(filepath.Join(home, ".graph-ops"))
	for _, e := range entries {
		if e.Name() != "machine-id" {
			t.Fatalf("unexpected file %s", e.Name())
		}
	}
}

func TestMachineIDConcurrentFirstUseAgrees(t *testing.T) {
	home := t.TempDir()
	const n = 16
	ids := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			a, err := Resolve(home)
			ids[i], errs[i] = a.MachineID, err
		}(i)
	}
	close(start)
	wg.Wait()
	for i := range ids {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("goroutine %d got %s, goroutine 0 got %s", i, ids[i], ids[0])
		}
	}
	saved, err := MachineID(home)
	if err != nil || saved != ids[0] {
		t.Fatalf("saved %q (%v), want %q", saved, err, ids[0])
	}
}

func TestBrokenMachineIDIsAnErrorAndLeftAlone(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".graph-ops", "machine-id")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not-a-uuid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(home); err == nil {
		t.Fatal("want an error for a broken machine-id file")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "not-a-uuid\n" {
		t.Fatalf("the broken file was rewritten: %q", data)
	}
}

func TestMachineIDNeedsAHome(t *testing.T) {
	if _, err := MachineID(""); err == nil {
		t.Fatal("want an error without a home directory")
	}
}

func TestNewSessionIDIsAUniqueUUIDv4(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := NewSessionID()
		if !uuidV4.MatchString(id) {
			t.Fatalf("%q is not a UUID v4", id)
		}
		if seen[id] {
			t.Fatalf("%q repeated", id)
		}
		seen[id] = true
	}
}

// DFLT-00336: the name is sanitized and capped when it is resolved, since
// other members' machines print it.
func TestResolveSanitizesAndCapsMyName(t *testing.T) {
	home := t.TempDir()
	writeHomeConfig(t, home, "\x1b[2J\u202eAlice\nfake line"+strings.Repeat("x", 300))
	a, err := Resolve(home)
	if err != nil {
		t.Fatal(err)
	}
	if a.NameIsFallback || strings.ContainsAny(a.Name, "\x1b\n\u202e") ||
		!strings.HasPrefix(a.Name, "[2JAlice fake line") || utf8.RuneCountInString(a.Name) != displayname.MaxRunes {
		t.Fatalf("got %+v", a)
	}
}

// A myName made only of control or invisible characters is no name.
func TestResolveFallsBackWhenMyNameSanitizesToNothing(t *testing.T) {
	home := t.TempDir()
	writeHomeConfig(t, home, "\x1b\n\t\u200b\u202e")
	stubOS(t, "taro", nil, "mac01", nil)
	a, err := Resolve(home)
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "taro@mac01" || !a.NameIsFallback {
		t.Fatalf("got %+v, want taro@mac01 (fallback)", a)
	}
}

// The OS user and host names are sanitized too; a part that becomes empty
// is "unknown", and the whole is capped.
func TestResolveSanitizesTheFallbackParts(t *testing.T) {
	cases := []struct{ user, host, want string }{
		{"ta\x1b[1mro", "mac\n01", "ta[1mro@mac 01"},
		{"DOMAIN\\ta\u202ero", "mac01", "taro@mac01"},
		{"\x1b\x07", "mac01", "unknown@mac01"},
		{"taro", "\u200b\t", "taro@unknown"},
		{"山田", "mac01", "山田@mac01"},
	}
	for _, c := range cases {
		home := t.TempDir()
		stubOS(t, c.user, nil, c.host, nil)
		a, err := Resolve(home)
		if err != nil {
			t.Fatal(err)
		}
		if a.Name != c.want || !a.NameIsFallback {
			t.Fatalf("user %q host %q: got %+v, want %s (fallback)", c.user, c.host, a, c.want)
		}
	}
	home := t.TempDir()
	stubOS(t, strings.Repeat("u", 100), nil, strings.Repeat("h", 100), nil)
	a, _ := Resolve(home)
	if n := utf8.RuneCountInString(a.Name); n != displayname.MaxRunes || !strings.HasPrefix(a.Name, strings.Repeat("u", 100)+"@") {
		t.Fatalf("long parts: %q (%d runes)", a.Name, n)
	}
}
