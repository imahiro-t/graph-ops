package runner

import (
	"errors"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/identity"
	"github.com/graph-ops/core-go/internal/store"
)

// This file connects the autopilot's shared runs (DFLT-00326) to the data
// source: an autopilot.SharedRuns over a store.AutopilotRunStore, and who is
// acting (internal/identity).

// StoreSharedRuns adapts a store.AutopilotRunStore to autopilot.SharedRuns,
// converting records to runs and back (Run.ToRecord / RunFromRecord) and
// store.ErrAutopilotRunsUnsupported to autopilot.ErrSharedRunsUnsupported.
type StoreSharedRuns struct {
	Store store.AutopilotRunStore
	// Logf reports a shared record that cannot be read (it is skipped).
	Logf func(format string, args ...any)
}

var _ autopilot.SharedRuns = StoreSharedRuns{}

func sharedErr(err error) error {
	if errors.Is(err, store.ErrAutopilotRunsUnsupported) {
		return autopilot.ErrSharedRunsUnsupported
	}
	return err
}

func (s StoreSharedRuns) runs(recs []domain.AutopilotRunRecord) []*autopilot.Run {
	out := make([]*autopilot.Run, 0, len(recs))
	for _, rec := range recs {
		r, err := autopilot.RunFromRecord(rec)
		if err != nil {
			if s.Logf != nil {
				s.Logf("skipping an unreadable shared autopilot run: %v", err)
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// List implements autopilot.SharedRuns.
func (s StoreSharedRuns) List(projectID string) ([]*autopilot.Run, error) {
	recs, err := s.Store.ListAutopilotRuns(projectID)
	if err != nil {
		return nil, sharedErr(err)
	}
	return s.runs(recs), nil
}

// Begin implements autopilot.SharedRuns.
func (s StoreSharedRuns) Begin(projectID string, decide func(shared []*autopilot.Run) (*autopilot.Run, error)) error {
	err := s.Store.BeginAutopilotRun(projectID, func(existing []domain.AutopilotRunRecord) (*domain.AutopilotRunRecord, error) {
		run, err := decide(s.runs(existing))
		if err != nil || run == nil {
			return nil, err
		}
		rec, err := run.ToRecord()
		if err != nil {
			return nil, err
		}
		return &rec, nil
	})
	return sharedErr(err)
}

// Save implements autopilot.SharedRuns.
func (s StoreSharedRuns) Save(run *autopilot.Run) error {
	rec, err := run.ToRecord()
	if err != nil {
		return err
	}
	return sharedErr(s.Store.SaveAutopilotRun(rec))
}

// Delete implements autopilot.SharedRuns.
func (s StoreSharedRuns) Delete(projectID, runID string) error {
	return sharedErr(s.Store.DeleteAutopilotRun(runID))
}

// IdentityActor resolves homeDir's actor (internal/identity) as the
// autopilot's StartedBy.
func IdentityActor(homeDir string) func() (*autopilot.StartedBy, error) {
	return func() (*autopilot.StartedBy, error) {
		a, err := identity.Resolve(homeDir)
		if err != nil {
			return nil, err
		}
		return &autopilot.StartedBy{Name: a.Name, NameIsFallback: a.NameIsFallback, MachineID: a.MachineID}, nil
	}
}

// actor resolves who is acting, once per Service: nil (and no error) when
// the Service has no ResolveActor.
func (s *Service) actor() (*autopilot.StartedBy, error) {
	if s.ResolveActor == nil {
		return nil, nil
	}
	s.actorOnce.Do(func() { s.actorVal, s.actorErr = s.ResolveActor() })
	return s.actorVal, s.actorErr
}

// shareRun writes run's shared copy after a local save (outside the
// registry lock). A failure does not stop the run: it is logged, and other
// members may see the run as interrupted until a later save gets through.
func (s *Service) shareRun(run *autopilot.Run) {
	shared := s.registry().Shared
	if shared == nil || run == nil {
		return
	}
	autopilot.LogSharedError(s.logf, "sharing autopilot run "+run.ID, shared.Save(run))
}
