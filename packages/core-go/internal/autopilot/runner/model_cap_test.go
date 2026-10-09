package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00375: a run's model cap is recorded by its start, kept or replaced
// by later starts, and passed as --model to every session the run launches.

func strp(s string) *string { return &s }

func (h *harness) startWithModel(root, mode, runID string, reserve bool, cap *string) StartResult {
	h.t.Helper()
	res, err := h.svc.StartWithModel(root, mode, runID, reserve, cap)
	if err != nil {
		h.t.Fatalf("StartWithModel(%s, %s, %q): %v", root, mode, runID, err)
	}
	return res
}

func (h *harness) setRun(runID string, fn func(run *autopilot.Run)) {
	h.t.Helper()
	if err := h.svc.Registry.WithLock(h.projectID, func(tx *autopilot.Tx) error {
		run, err := tx.Load(runID)
		if err != nil {
			return err
		}
		fn(run)
		return tx.Save(run)
	}); err != nil {
		h.t.Fatal(err)
	}
}

func hasModelArg(args []string, cap string) bool {
	joined := " " + strings.Join(args, " ") + " "
	if cap == "" {
		return !strings.Contains(joined, " --model ")
	}
	return strings.Contains(joined, " --model "+cap+" ")
}

func TestParseModelCap(t *testing.T) {
	for in, want := range map[string]*string{"": nil, "inherit": strp(""), "haiku": strp("haiku"), "Sonnet": strp("sonnet"), "OPUS": strp("opus")} {
		got, err := ParseModelCap(in)
		if err != nil || (got == nil) != (want == nil) || (got != nil && *got != *want) {
			t.Errorf("ParseModelCap(%q) = %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"fable", "gpt-4", "Inherit", "opus; rm -rf /"} {
		_, err := ParseModelCap(in)
		assertAPICode(t, err, domain.ErrCodeValidation)
	}
}

func TestModelCap_NewRunRecordsIt(t *testing.T) {
	for _, tc := range []struct {
		cap  *string
		want string
	}{{nil, ""}, {strp("sonnet"), "sonnet"}, {strp(""), ""}} {
		h := newHarness(t)
		r := h.ticket("R", "")
		res := h.startWithModel(r, autopilot.ModeTicket, "", false, tc.cap)
		if res.ModelCap != tc.want || h.run(res.RunID).ModelCap != tc.want {
			t.Errorf("cap %v: result %q, run %q, want %q", tc.cap, res.ModelCap, h.run(res.RunID).ModelCap, tc.want)
		}
	}
}

// A reserved, interrupted or stopped run recorded with sonnet, then started
// again (with --run, or -- interrupted/stopped only -- without it): no
// --model keeps sonnet, a model replaces it, inherit clears it.
func TestModelCap_AdoptionAndTakeover(t *testing.T) {
	type state string
	const (
		reserved    state = "reserved"
		interrupted state = "interrupted"
		stopped     state = "stopped"
	)
	cases := []struct {
		state   state
		withRun bool
		cap     *string
		want    string
	}{
		{reserved, true, nil, "sonnet"},
		{reserved, true, strp("haiku"), "haiku"},
		{reserved, true, strp(""), ""},
		{interrupted, true, nil, "sonnet"},
		{interrupted, true, strp("opus"), "opus"},
		{interrupted, true, strp(""), ""},
		{stopped, true, nil, "sonnet"},
		{stopped, true, strp("haiku"), "haiku"},
		{stopped, true, strp(""), ""},
		{interrupted, false, nil, "sonnet"},
		{interrupted, false, strp("opus"), "opus"},
		{interrupted, false, strp(""), ""},
		{stopped, false, nil, "sonnet"},
		{stopped, false, strp("haiku"), "haiku"},
		{stopped, false, strp(""), ""},
	}
	for _, tc := range cases {
		capName := "none"
		if tc.cap != nil {
			capName = "'" + *tc.cap + "'"
		}
		name := string(tc.state) + "/run=" + map[bool]string{true: "given", false: "absent"}[tc.withRun] + "/model=" + capName
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			r := h.ticket("R", "")
			first := h.startWithModel(r, autopilot.ModeTicket, "", tc.state == reserved, strp("sonnet"))
			switch tc.state {
			case interrupted:
				h.makeStale()
			case stopped:
				h.setRun(first.RunID, func(run *autopilot.Run) { run.State = autopilot.RunStopped })
			}
			runID := ""
			if tc.withRun {
				runID = first.RunID
			}
			res := h.startWithModel(r, autopilot.ModeTicket, runID, false, tc.cap)
			if res.RunID != first.RunID || res.Created {
				t.Fatalf("res = %+v, want run %s adopted or taken over", res, first.RunID)
			}
			if got := h.run(res.RunID).ModelCap; got != tc.want || res.ModelCap != tc.want {
				t.Errorf("model_cap = %q (result %q), want %q", got, res.ModelCap, tc.want)
			}
		})
	}
}

// A run with a cap launches every session -- work, merge-up, finalize --
// with --model <cap>; one without launches none with --model.
func TestModelCap_EverySessionIsLaunchedWithIt(t *testing.T) {
	for _, cap := range []string{"haiku", ""} {
		t.Run("cap="+cap, func(t *testing.T) {
			h := newHarness(t)
			h.settings.MainReflection = autopilot.MainReflectionPullRequest
			r := h.ticket("R", "")
			c := h.ticket("C", r)
			h.behave[c] = func(w *workerCall) {
				w.commit()
				w.release()
				// R's branch moves on by hand: C needs a merge-up session.
				rt := h.st(w.RunID, r)
				commitIn(t, rt.Worktree, "human.txt", "x", "human commit on R")
				commitIn(t, w.WorkDir, "more.txt", "y", "more on C")
				w.report(autopilot.TicketDone, "c")
			}
			run := h.startWithModel(r, autopilot.ModeTree, "", false, strp(cap))
			h.drive(run.RunID, nil)
			roles := map[string]bool{}
			for _, call := range h.launches {
				roles[call.Role] = true
				if !hasModelArg(call.Args, cap) {
					t.Errorf("%s %s launched with %q, want cap %q", call.Role, call.Ticket, call.Args, cap)
				}
				if !strings.HasPrefix(strings.Join(call.Args, " "), "--permission-mode ") {
					t.Errorf("args = %q, want the permission mode first", call.Args)
				}
			}
			for _, role := range []string{autopilot.RoleWork, autopilot.RoleMergeUp, autopilot.RoleFinalize} {
				if !roles[role] {
					t.Errorf("no %s session was launched: %v", role, h.actions)
				}
			}
		})
	}
}

// A grandchild created while its parent is being processed gets the same
// cap, and so does every worker-context.
func TestModelCap_GrandchildAndWorkerContext(t *testing.T) {
	h := newHarness(t)
	r := h.ticket("R", "")
	c := h.ticket("C", r)
	var g string
	contexts := map[string]string{}
	h.launchWorker = func(w *workerCall) {
		contexts[w.Ticket+"/"+w.Role] = w.ctx().ModelCap
		if w.Ticket == c && w.Role == autopilot.RoleWork {
			g = w.child("G")
		}
		defaultWorker(w)
	}
	run := h.startWithModel(r, autopilot.ModeTree, "", false, strp("sonnet"))
	h.drive(run.RunID, nil)
	if g == "" || !contains(h.workLaunches(), g) {
		t.Fatalf("grandchild not launched: %v", h.workLaunches())
	}
	for _, call := range h.launches {
		if !hasModelArg(call.Args, "sonnet") {
			t.Errorf("%s %s launched with %q", call.Role, call.Ticket, call.Args)
		}
	}
	for k, v := range contexts {
		if v != "sonnet" {
			t.Errorf("worker-context of %s: model_cap = %q", k, v)
		}
	}
	// worker-context always carries the key, even without a cap.
	raw, _ := json.Marshal(WorkerContext{})
	if !strings.Contains(string(raw), `"model_cap":""`) {
		t.Errorf("WorkerContext JSON = %s", raw)
	}
}

func TestModelCap_StatusAndSummary(t *testing.T) {
	h := newHarness(t)
	r := h.ticket("R", "")
	run := h.startWithModel(r, autopilot.ModeTicket, "", false, strp("sonnet"))
	statuses, err := h.svc.Status(h.projectID)
	if err != nil || len(statuses) != 1 || statuses[0].ModelCap != "sonnet" {
		t.Fatalf("status = %+v, %v", statuses, err)
	}
	if md := RenderSummary(h.run(run.RunID), nil); !strings.Contains(md, "- Model cap: sonnet\n") {
		t.Errorf("summary lacks the cap:\n%s", md)
	}
	h2 := newHarness(t)
	r2 := h2.ticket("R", "")
	run2 := h2.start(r2, autopilot.ModeTicket)
	if md := RenderSummary(h2.run(run2.RunID), nil); strings.Contains(md, "Model cap") {
		t.Errorf("uncapped summary mentions a cap:\n%s", md)
	}
}

// A run file from before model_cap reads as no cap; a doctored value is
// never passed on.
func TestModelCap_OldAndDoctoredRunFiles(t *testing.T) {
	h := newHarness(t)
	r := h.ticket("R", "")
	run := h.start(r, autopilot.ModeTicket)
	dir := filepath.Join(h.svc.Registry.Root, h.projectID, "runs")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Skipf("run files not where expected (%v)", err)
	}
	path := filepath.Join(dir, entries[0].Name())
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "model_cap") {
		t.Fatalf("an uncapped run file has model_cap: %s", raw)
	}
	if got := h.run(run.RunID); got.ModelCap != "" || got.ValidModelCap() != "" {
		t.Errorf("model_cap = %q", got.ModelCap)
	}
	h.setRun(run.RunID, func(r *autopilot.Run) { r.ModelCap = "opus; rm -rf /" })
	if got := h.run(run.RunID).ValidModelCap(); got != "" {
		t.Errorf("ValidModelCap of a doctored value = %q", got)
	}
}
