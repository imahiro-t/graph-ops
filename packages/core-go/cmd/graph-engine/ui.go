package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/graph-ops/core-go/internal/browser"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
)

const (
	uiServerStartTimeout = 10 * time.Second
	uiServerPollInterval = 200 * time.Millisecond
	uiHealthCheckTimeout = 1500 * time.Millisecond
	// Applied to the two calls this command makes against the already-running
	// server (GET /api/projects, PUT /api/current-project). Longer than the
	// health check: by then the server is known to be up, so a slow answer is
	// worth waiting for rather than a signal that nothing is listening.
	uiAPIRequestTimeout = 5 * time.Second
)

// cmdUI implements `graph-engine ui`, GOPS-00001's `/ui` slash command: open
// the local Web UI, in the default browser, on whichever project
// corresponds to the current working directory -- starting the UI server
// itself first if it isn't already running. See the execution plan
// (art-630d82eb section 4.1) for the full design; this follows it directly:
//
//  1. Resolve the current working directory and look it up against every
//     project's local path as the running server reports it (GET
//     /api/projects' local_path, i.e. the server's home config file
//     projectPaths -- DFLT-00080), deepest containing path first.
//  2. Health-check the UI server (GET /api/health); if it doesn't respond,
//     start it in the background (this same binary, `serve`, detached) and
//     poll until it comes up or uiServerStartTimeout elapses. This is the
//     ONLY error path this command has (completion criterion 3: "起動失敗
//     時のみエラーメッセージを表示する").
//  3. Once the server is confirmed up: if a project matched, switch the
//     server's "current project" to it (PUT /api/current-project) and open
//     the UI's root URL; otherwise open the root URL with a
//     newProject=1&workDir=<dir> query the Web UI reads on mount to
//     auto-open its project-setup dialog for that directory, where the user
//     either creates a new project or picks an existing one (see
//     packages/web/src/components/ProjectSetupModal.tsx). The query name
//     workDir is kept as-is for compatibility.
//
// Opening the browser itself is deliberately best-effort: a failure there
// is reported as a warning (with the URL printed for the user to open by
// hand), never as a command failure -- completion criterion 3 only calls
// out server-startup failure as an error case.
func cmdUI(rc runtimeConfig, args []string) error {
	targetDir, err := filepath.Abs(rc.WorkDir)
	if err != nil {
		return err
	}
	targetDir = filepath.Clean(targetDir)

	// Two URLs for the same server, deliberately: baseURL is the one THIS
	// process connects to, so it must be built from rc.Host -- the address
	// the server binds (see clientBaseURL, and DFLT-00023 item N-1 for the
	// bug that came of hardcoding "localhost" here). browserBaseURL is the
	// one handed to the browser and printed, which prefers the friendlier
	// "localhost" wherever that really does reach the same listener (see
	// humanBaseURL). With the default loopback host the two differ only
	// cosmetically; with a concrete non-loopback host, using the hardcoded
	// one for the health check is what made `ui` believe no server was
	// running and try to start a second one.
	baseURL := clientBaseURL(rc.Host, rc.Port)
	browserBaseURL := humanBaseURL(rc.Host, rc.Port)

	if !uiHealthCheck(baseURL) {
		if err := startUIServerInBackground(rc.Host, rc.Port); err != nil {
			return fmt.Errorf("failed to start the UI server: %w", err)
		}
		if !waitForUIServer(baseURL, uiServerStartTimeout) {
			return fmt.Errorf(
				"UI server did not become ready at %s within %s (it may have failed to start, e.g. the port is already in use by something else) -- see the startup log for details",
				baseURL, uiServerStartTimeout,
			)
		}
	}

	// Deliberately queried from the already-running server (GET
	// /api/projects) rather than opened as this CLI process's own local
	// repo.ListProjects(): this process's own DBPath resolves relative to
	// its cwd (see loadRuntimeConfig), which need not be the directory the
	// long-running `serve` process was originally started from and so need
	// not be the same DB file at all. Going through the server's own API
	// guarantees the project this matches against is the same one
	// switchCurrentProjectViaAPI below will actually try to switch to --
	// otherwise a project found in this process's local DB can be entirely
	// unknown to the server, which switchCurrentProjectViaAPI would then
	// report as a 404.
	projects, err := fetchProjectsViaAPI(baseURL)
	if err != nil {
		return err
	}
	matched := findProjectByLocalPath(projects, targetDir)

	targetURL := resolveTargetURL(browserBaseURL, targetDir, matched)
	if matched != nil {
		if err := switchCurrentProjectViaAPI(baseURL, matched.ID); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: found project %s but failed to switch to it: %v\n", matched.ID, err)
		}
	}

	if err := browser.Open(targetURL); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to open a browser automatically (%v). Open this URL manually:\n%s\n", err, targetURL)
		return nil
	}
	fmt.Println(targetURL)
	return nil
}

// uiProject is one element of GET /api/projects as this command decodes it:
// the DB project plus the server environment's local path for it ("" when
// unset -- such a project never matches a directory).
type uiProject struct {
	domain.Project
	LocalPath string `json:"local_path"`
}

// findProjectByLocalPath returns the project whose local path is targetDir
// (already cleaned/absolute) or contains it, the deepest such path winning
// -- the same rule create-ticket uses (runtimeconfig.FindProjectIDForDir),
// so running `ui` from a subdirectory such as a git worktree selects the
// enclosing project instead of opening the setup dialog. Symlinks are not
// resolved; a tie between projects sharing the same path goes to the one
// listed first.
func findProjectByLocalPath(projects []uiProject, targetDir string) *uiProject {
	paths := make(map[string]string, len(projects))
	ids := make([]string, 0, len(projects))
	for _, p := range projects {
		ids = append(ids, p.ID)
		if p.LocalPath != "" {
			if _, dup := paths[p.ID]; !dup {
				paths[p.ID] = p.LocalPath
			}
		}
	}
	id := runtimeconfig.FindProjectIDForDir(paths, ids, targetDir)
	if id == "" {
		return nil
	}
	for i := range projects {
		if projects[i].ID == id {
			return &projects[i]
		}
	}
	return nil
}

// resolveTargetURL builds the URL `ui` opens: the UI's root when an existing
// project matched (its current-project switch is a separate, best-effort
// step handled by the caller), or the root with a newProject=1&workDir=...
// query the Web UI reads on mount to auto-open its project-setup dialog
// (create a new project, or pick an existing one) for targetDir, when
// nothing matched.
func resolveTargetURL(baseURL, targetDir string, matched *uiProject) string {
	if matched != nil {
		return baseURL + "/"
	}
	return baseURL + "/?newProject=1&workDir=" + url.QueryEscape(targetDir)
}

// uiHealthCheck reports whether a UI server is already listening at baseURL
// by hitting its existing GET /api/health endpoint with a short timeout.
func uiHealthCheck(baseURL string) bool {
	client := http.Client{Timeout: uiHealthCheckTimeout}
	resp, err := client.Get(baseURL + "/api/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// waitForUIServer polls uiHealthCheck until it succeeds or timeout elapses,
// checking one final time right at the deadline before giving up.
func waitForUIServer(baseURL string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if uiHealthCheck(baseURL) {
			return true
		}
		time.Sleep(uiServerPollInterval)
	}
	return uiHealthCheck(baseURL)
}

// startUIServerInBackground launches `<this binary> serve --host <host>
// --port <port>` as a detached background process (see detachSysProcAttr,
// OS-specific) so it keeps running after this `ui` command exits. Its stdout/stderr are
// redirected to a log file under the user's own $HOME/.graph-ops directory
// (see uiServerLogPath) rather than discarded, so a startup failure that
// races past waitForUIServer's timeout is still diagnosable after the fact.
//
// That log file deliberately does NOT live under os.TempDir(): on a
// multi-user Linux box /tmp is shared and typically world-writable, so a
// fixed, predictable path there (as this used to use) lets another local
// user pre-plant a symlink at that path and have this process follow it
// (CWE-59/CWE-377), and the previous 0o644 mode made the server's
// stdout/stderr -- which can include DB errors and paths -- readable by
// every local user (information disclosure). See security-review-verdict
// art-06920ead for the finding this fixes. $HOME/.graph-ops is already this
// project's per-user root (config.yaml, extensions/, ...), is not
// shared/world-writable, and the log file itself is opened 0o600.
//
// host is passed explicitly rather than left to the child process's own
// loadRuntimeConfig: the child would resolve the same value anyway (it
// inherits this process's cwd and environment, which is all that resolution
// looks at), but leaving it implicit is what let cmdUI's own base URL drift
// away from the address the server actually binds in the first place
// (DFLT-00023 item N-1). Passing it makes the parent's baseURL and the
// child's listener provably the same address.
func startUIServerInBackground(host string, port int) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	logPath, err := uiServerLogPath()
	if err != nil {
		return err
	}
	// O_EXCL is intentionally not used here: the log is meant to
	// accumulate across repeated `ui` invocations for diagnosability, and
	// once the directory is the user's own $HOME/.graph-ops (created below
	// with 0o700, not a shared temp directory) there is no other local
	// user who could have planted a symlink at this path to follow.
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	// OpenFile's mode argument only applies when the file is created, so a
	// log file left over from before this fix (0o644) needs to be
	// tightened explicitly too.
	if err := logFile.Chmod(0o600); err != nil {
		return err
	}

	cmd := exec.Command(self, "serve", "--host", host, "--port", strconv.Itoa(port))
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = detachSysProcAttr()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launching %s serve (see %s for output): %w", self, logPath, err)
	}
	// Deliberately not cmd.Wait()'d -- `serve` runs until the process is
	// killed, and this command must return as soon as the server is
	// confirmed reachable (see waitForUIServer), not block forever on it.
	return nil
}

// uiServerLogPath returns the path of the background `serve` process's
// stdout/stderr log, creating its parent directory ($HOME/.graph-ops, this
// project's existing per-user config root -- see internal/config -- with
// user-only 0o700 permissions if it doesn't exist yet) along the way. See
// startUIServerInBackground's doc comment for why this is under the user's
// home directory rather than os.TempDir().
func uiServerLogPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".graph-ops")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "ui-serve.log"), nil
}

// fetchProjectsViaAPI calls the running server's own GET /api/projects,
// returning it the same registry switchCurrentProjectViaAPI (PUT
// /api/current-project) will act against -- see cmdUI's comment on why this
// isn't repo.ListProjects() opened directly by this CLI process.
func fetchProjectsViaAPI(baseURL string) ([]uiProject, error) {
	client := http.Client{Timeout: uiAPIRequestTimeout}
	resp, err := client.Get(baseURL + "/api/projects")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /api/projects: unexpected status %d", resp.StatusCode)
	}
	var projects []uiProject
	if err := json.NewDecoder(resp.Body).Decode(&projects); err != nil {
		return nil, err
	}
	return projects, nil
}

// switchCurrentProjectViaAPI calls the running server's own PUT
// /api/current-project (rather than repo.SetCurrentProjectID directly), so
// this write always goes through the one process actually holding the DB
// connection the rest of the running server uses -- consistent with how the
// Web UI's own project switcher does it.
func switchCurrentProjectViaAPI(baseURL, projectID string) error {
	body, err := json.Marshal(map[string]string{"project_id": projectID})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPut, baseURL+"/api/current-project", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// Every state-changing request must carry this, or the server's CORS
	// middleware rejects it with 403 (see httpserver/server.go's
	// csrfHeaderName -- it's unexported there, so the literal is repeated
	// here rather than imported).
	req.Header.Set("X-Graph-Engine-Client", "1")

	client := http.Client{Timeout: uiAPIRequestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("PUT /api/current-project: unexpected status %d", resp.StatusCode)
	}
	return nil
}
