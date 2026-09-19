package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// localState is the plugin's own small state file. It holds the two things
// that are global to the data source rather than to one Jira project, and so
// have no natural home in Jira:
//
//   - which Jira projects are registered as GraphOps projects (and the key of
//     each one's metadata issue), in registration order -- ListProjects;
//   - the current project -- GetCurrentProjectID/SetCurrentProjectID.
//
// Everything else lives in Jira. Registering a project again after the file
// is lost finds and reuses the project's existing metadata issue, so no Jira
// data is orphaned by losing this file.
type localState struct {
	Projects         []registeredProject `json:"projects"`
	CurrentProjectID string              `json:"current_project_id"`
}

type registeredProject struct {
	Key          string `json:"key"`
	MetaIssueKey string `json:"meta_issue_key"`
}

type stateFile struct {
	path string
	mu   sync.Mutex
}

func (f *stateFile) load() (localState, error) {
	raw, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return localState{}, nil
	}
	if err != nil {
		return localState{}, err
	}
	var s localState
	if err := json.Unmarshal(raw, &s); err != nil {
		return localState{}, err
	}
	return s, nil
}

// save writes the file atomically (temp file + rename), user-only.
func (f *stateFile) save(s localState) error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".jira-datasource-state-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, f.path)
}

// update runs fn on the current state under the file's lock and saves the
// result if fn returns nil.
func (f *stateFile) update(fn func(*localState) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.load()
	if err != nil {
		return err
	}
	if err := fn(&s); err != nil {
		return err
	}
	return f.save(s)
}

func (f *stateFile) read() (localState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.load()
}
