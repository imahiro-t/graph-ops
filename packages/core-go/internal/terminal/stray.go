package terminal

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DFLT-00379: the tab script looks for a stray tab -- the first attempt's
// tab appearing late, after the retry's tab was taken -- for only 1 second
// before `do script`, so as not to make every retried launch wait longer.
// A stray tab appearing after that second is looked for afterwards instead,
// with one ps run and no Apple event to Terminal: ReadStraySnapshot and
// StraySnapshot.LateStrayTTYs.
//
// What ps can tell, and what it cannot: when Terminal.app opens a tab, the
// first process on the tab's tty is `login`, whose parent is Terminal.app
// itself, and the login shell it starts. So a tab of Terminal.app that
// opened within a given span of time and is still empty is a tty whose
// oldest process is a `login` child of Terminal.app started in that span,
// with nothing but `login` and shells on it. Other terminals (iTerm2, VS
// Code's integrated terminal, tmux) start their shells without such a
// `login`, so they are never taken. Which Terminal window a tab is in is
// not something ps can tell, so a tab the user opened in any Terminal
// window within the span and left empty is taken as well; see
// docs/autopilot.md.

// strayPSTimeout bounds the one ps run of ReadStraySnapshot.
const strayPSTimeout = 2 * time.Second

// strayEtimeSlack is the room left on both ends of the span for ps's etime,
// which counts whole seconds.
const strayEtimeSlack = time.Second

// strayShells are the login shells an empty tab may have on its tty next to
// `login` (by the basename of the command, a leading "-" removed).
var strayShells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "tcsh": true, "csh": true, "ksh": true, "dash": true,
}

// terminalAppSuffix ends the command of Terminal.app's own process.
const terminalAppSuffix = "/Terminal.app/Contents/MacOS/Terminal"

// strayPSLine is one line of `ps -A -o tty=,pid=,ppid=,etime=,command=`:
// the command is the rest of the line, spaces included.
var strayPSLine = regexp.MustCompile(`^\s*(\S+)\s+([0-9]+)\s+([0-9]+)\s+([0-9:-]+)\s+(.*)$`)

// strayTTYPattern is the only tty column a candidate may have: a
// pseudo-terminal's name as ps prints it, without /dev/.
var strayTTYPattern = regexp.MustCompile(`^ttys[0-9]+$`)

// StraySnapshot is what one ps run tells about the processes on the
// pseudo-terminals (DFLT-00379), read once by ReadStraySnapshot and then
// asked, look by look, for the stray tabs of a span (LateStrayTTYs) -- so a
// poll with several looks due runs ps once.
type StraySnapshot struct {
	terminalPIDs map[int]bool
	byTTY        map[string][]strayProc
}

// ReadStraySnapshot runs `ps -A -o tty=,pid=,ppid=,etime=,command=` once,
// bounded to strayPSTimeout, and reads its output. An error means ps could
// not be run or its output read. Take the time the elapsed times are
// counted back from (LateStrayTTYs' now) right after it returns.
func ReadStraySnapshot() (StraySnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), strayPSTimeout)
	defer cancel()
	output, err := runCommand(ctx, "ps", "-A", "-o", "tty=,pid=,ppid=,etime=,command=")
	if err != nil {
		return StraySnapshot{}, fmt.Errorf("ps: %w", err)
	}
	return parseStraySnapshot(string(output))
}

// strayProc is one process of a strayPSLine.
type strayProc struct {
	pid, ppid int
	elapsed   time.Duration
	command   string
}

// commandBase is the basename of a command's first word, a leading "-" (a
// login shell) removed.
func commandBase(command string) string {
	first := command
	if i := strings.IndexAny(first, " \t"); i >= 0 {
		first = first[:i]
	}
	return strings.TrimPrefix(path.Base(first), "-")
}

// parseEtime reads ps's etime, [[dd-]hh:]mm:ss.
func parseEtime(s string) (time.Duration, bool) {
	days := 0
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.Atoi(d)
		if err != nil || n < 0 {
			return 0, false
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 || (days > 0 && len(parts) != 3) {
		return 0, false
	}
	total := 0
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return 0, false
		}
		total = total*60 + n
	}
	return time.Duration(days*86400+total) * time.Second, true
}

// parseStraySnapshot reads ps's output into a StraySnapshot. Lines that
// are not a process line (a warning ps wrote to stderr, which runCommand's
// combined output carries) are skipped; output with no process line at all
// is an error.
func parseStraySnapshot(output string) (StraySnapshot, error) {
	sn := StraySnapshot{terminalPIDs: map[int]bool{}, byTTY: map[string][]strayProc{}}
	parsed := 0
	for _, line := range strings.Split(output, "\n") {
		m := strayPSLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		pid, err1 := strconv.Atoi(m[2])
		ppid, err2 := strconv.Atoi(m[3])
		elapsed, ok := parseEtime(m[4])
		if err1 != nil || err2 != nil || !ok {
			continue
		}
		parsed++
		command := strings.TrimSpace(m[5])
		if strings.HasSuffix(command, terminalAppSuffix) || strings.Contains(command, terminalAppSuffix+" ") {
			sn.terminalPIDs[pid] = true
		}
		if !strayTTYPattern.MatchString(m[1]) {
			continue
		}
		sn.byTTY[m[1]] = append(sn.byTTY[m[1]], strayProc{pid: pid, ppid: ppid, elapsed: elapsed, command: command})
	}
	if parsed == 0 {
		return StraySnapshot{}, fmt.Errorf("ps: no process line in its output")
	}
	return sn, nil
}

// LateStrayTTYs returns the ttys (/dev/ttysN, sorted) of the empty
// Terminal.app tabs in the snapshot that opened between from and until and
// are not in exclude (see the comment at the top of this file for what is
// taken). now is the time the processes' elapsed times are counted back
// from: the time ps ran, on the same clock from and until come from. No
// Terminal.app process in the snapshot means no candidate.
func (sn StraySnapshot) LateStrayTTYs(from, until time.Time, exclude []string, now time.Time) []string {
	if len(sn.terminalPIDs) == 0 {
		return nil
	}
	excluded := map[string]bool{}
	for _, tty := range exclude {
		excluded[tty] = true
	}
	var found []string
	for tty, procs := range sn.byTTY {
		dev := "/dev/" + tty
		if excluded[dev] {
			continue
		}
		oldest := time.Duration(-1)
		empty := true
		for _, p := range procs {
			if p.elapsed > oldest {
				oldest = p.elapsed
			}
			if b := commandBase(p.command); b != "login" && !strayShells[b] {
				empty = false
			}
		}
		if !empty {
			continue
		}
		// The oldest process must be a login started by Terminal.app (ties
		// are common: login and its shell start within the same second).
		terminalLogin := false
		for _, p := range procs {
			if p.elapsed == oldest && commandBase(p.command) == "login" && sn.terminalPIDs[p.ppid] {
				terminalLogin = true
			}
		}
		if !terminalLogin {
			continue
		}
		started := now.Add(-oldest)
		if started.Before(from.Add(-strayEtimeSlack)) || started.After(until.Add(strayEtimeSlack)) {
			continue
		}
		found = append(found, dev)
	}
	sort.Strings(found)
	return found
}
