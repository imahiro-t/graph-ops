// Command jira-datasource is a sample GraphOps HTTP data source plugin that
// stores GraphOps tickets, execution graphs, artifacts, projects and labels
// in Jira Cloud. It implements docs/http-datasource/openapi.yaml; see
// README.md for how GraphOps data maps onto Jira and for its limitations.
//
// Configuration is read from environment variables only, so no secret ever
// has to be written to a file:
//
//	JIRA_BASE_URL              https://<your-site>.atlassian.net (required)
//	JIRA_EMAIL                 the Atlassian account email (required)
//	JIRA_API_TOKEN             an Atlassian API token for that account (required)
//	GRAPHOPS_DATASOURCE_TOKEN  the bearer token graph-engine must send (required)
//	LISTEN_ADDR                default 127.0.0.1:8787
//	JIRA_ISSUE_TYPE            issue type for ticket issues, default Task
//	JIRA_SUBTASK_ISSUE_TYPE    sub-task issue type for node sub-tasks, default Subtask
//	JIRA_NODE_IN_PROGRESS_STATUS
//	                           workflow status a node sub-task is moved to while
//	                           the node is under way, default "In Progress"
//	JIRA_NODE_DONE_STATUS      workflow status a node sub-task is moved to when
//	                           the node is DONE, default "Done"
//	GRAPHOPS_STATE_FILE        local state file, default ./jira-datasource-state.json
//
// Setting either status variable to the empty string (set, but empty)
// turns that workflow move off; the node's status label is kept up to date
// either way.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type config struct {
	JiraBaseURL     string
	JiraEmail       string
	JiraAPIToken    string
	DataSourceToken string
	ListenAddr      string
	IssueType       string
	StateFile       string
	// Node sub-tasks (see storeOptions).
	SubtaskIssueType string
	InProgressStatus string
	DoneStatus       string
}

const (
	defaultListenAddr = "127.0.0.1:8787"
	defaultIssueType  = "Task"
	defaultStateFile  = "jira-datasource-state.json"

	defaultSubtaskIssueType = "Subtask"
	defaultInProgressStatus = "In Progress"
	defaultDoneStatus       = "Done"
)

// loadConfig reads the configuration through lookupEnv (os.LookupEnv
// outside tests) and reports the first missing or invalid value by name.
// lookupEnv, unlike os.Getenv, tells an unset variable (default value) from
// one set to "" (for the node status names: no workflow move).
func loadConfig(lookupEnv func(string) (string, bool)) (config, error) {
	getenv := func(k string) string { v, _ := lookupEnv(k); return v }
	statusName := func(k, def string) string {
		v, ok := lookupEnv(k)
		if !ok {
			return def
		}
		return strings.TrimSpace(v)
	}
	c := config{
		JiraBaseURL:     strings.TrimSpace(getenv("JIRA_BASE_URL")),
		JiraEmail:       strings.TrimSpace(getenv("JIRA_EMAIL")),
		JiraAPIToken:    getenv("JIRA_API_TOKEN"),
		DataSourceToken: getenv("GRAPHOPS_DATASOURCE_TOKEN"),
		ListenAddr:      strings.TrimSpace(getenv("LISTEN_ADDR")),
		IssueType:       strings.TrimSpace(getenv("JIRA_ISSUE_TYPE")),
		StateFile:       strings.TrimSpace(getenv("GRAPHOPS_STATE_FILE")),

		SubtaskIssueType: strings.TrimSpace(getenv("JIRA_SUBTASK_ISSUE_TYPE")),
		InProgressStatus: statusName("JIRA_NODE_IN_PROGRESS_STATUS", defaultInProgressStatus),
		DoneStatus:       statusName("JIRA_NODE_DONE_STATUS", defaultDoneStatus),
	}
	for _, req := range []struct{ name, value string }{
		{"JIRA_BASE_URL", c.JiraBaseURL},
		{"JIRA_EMAIL", c.JiraEmail},
		{"JIRA_API_TOKEN", c.JiraAPIToken},
		{"GRAPHOPS_DATASOURCE_TOKEN", c.DataSourceToken},
	} {
		if req.value == "" {
			return config{}, fmt.Errorf("environment variable %s is required", req.name)
		}
	}
	u, err := url.Parse(c.JiraBaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname()))) {
		return config{}, errors.New("JIRA_BASE_URL must be an https:// URL such as https://your-site.atlassian.net")
	}
	if c.ListenAddr == "" {
		c.ListenAddr = defaultListenAddr
	}
	if c.IssueType == "" {
		c.IssueType = defaultIssueType
	}
	if c.StateFile == "" {
		c.StateFile = defaultStateFile
	}
	if c.SubtaskIssueType == "" {
		c.SubtaskIssueType = defaultSubtaskIssueType
	}
	return c, nil
}

func (c config) storeOptions() storeOptions {
	return storeOptions{
		IssueType: c.IssueType, SubtaskIssueType: c.SubtaskIssueType,
		InProgressStatus: c.InProgressStatus, DoneStatus: c.DoneStatus,
	}
}

func describeStatus(name string) string {
	if name == "" {
		return "no workflow status (off)"
	}
	return strconv.Quote(name)
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func main() {
	logger := log.New(os.Stderr, "jira-datasource: ", log.LstdFlags)
	cfg, err := loadConfig(os.LookupEnv)
	if err != nil {
		logger.Fatal(err)
	}
	host, _, err := net.SplitHostPort(cfg.ListenAddr)
	if err != nil {
		logger.Fatalf("LISTEN_ADDR %q: %v", cfg.ListenAddr, err)
	}
	if !isLoopback(host) {
		// This sample serves plain HTTP. graph-engine only accepts plain
		// HTTP on loopback; anything else needs TLS in front of it.
		logger.Printf("warning: listening on %s without TLS; graph-engine will only connect to it over https:// through a TLS-terminating proxy", cfg.ListenAddr)
	}

	if len(cfg.DataSourceToken) < minRecommendedTokenLength {
		logger.Printf("warning: GRAPHOPS_DATASOURCE_TOKEN is shorter than %d characters; use a long random value (e.g. openssl rand -hex 32)", minRecommendedTokenLength)
	}

	jira := newJiraClient(cfg.JiraBaseURL, cfg.JiraEmail, cfg.JiraAPIToken)
	jira.logf = logger.Printf
	store := newStore(jira, cfg.StateFile, cfg.storeOptions())
	store.logf = logger.Printf
	logger.Printf("tickets are %q issues, nodes are %q sub-tasks; node sub-tasks move to %s (in progress) and %s (done)",
		cfg.IssueType, cfg.SubtaskIssueType, describeStatus(cfg.InProgressStatus), describeStatus(cfg.DoneStatus))
	srv := newServer(cfg.ListenAddr, newHandler(store, cfg.DataSourceToken, logger))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	logger.Printf("serving the GraphOps data source protocol %s on http://%s (state file %s)", protocolVersion, cfg.ListenAddr, cfg.StateFile)
	select {
	case err := <-errc:
		logger.Fatal(err)
	case <-ctx.Done():
	}
	// Graceful shutdown: stop accepting connections and let requests in
	// flight finish (each is bounded by requestDeadline anyway).
	logger.Printf("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), requestDeadline+5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Printf("shutdown: %v", err)
	}
}

// minRecommendedTokenLength is the bearer token length below which the
// plugin warns at startup (64 hex characters = 32 random bytes is a good
// value; the warning threshold is lower so that other encodings pass).
const minRecommendedTokenLength = 32

// newServer returns the HTTP server with timeouts on every phase of a
// connection, so a slow or idle client cannot hold one open indefinitely.
// WriteTimeout leaves room for requestDeadline plus writing the answer.
func newServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute, // request bodies are up to 64 MiB
		WriteTimeout:      requestDeadline + 2*time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
}
