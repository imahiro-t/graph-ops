package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/autopilot/runner"
	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
	"github.com/graph-ops/core-go/internal/store"
)

const (
	autopilotUsageLine = "usage: graph-engine autopilot <start|next|launch|wait|merge-up|worker-context|record-decision|" +
		"attach-decisions|touch|report|merge-into-parent|summary|status|settings> ..."
	autopilotSettingsUsageLine = "usage: graph-engine autopilot settings [--project <id>]"
	autopilotStartUsage        = "usage: graph-engine autopilot start <ticketId> --mode ticket|tree [--run <runId>]"
	autopilotNextUsage         = "usage: graph-engine autopilot next <runId>"
	autopilotLaunchUsage       = "usage: graph-engine autopilot launch <runId> <ticketId> [--role work|merge-up|finalize]"
	autopilotWaitUsage         = "usage: graph-engine autopilot wait <runId> <ticketId> [--timeout <duration>]"
	autopilotMergeUpUsage      = "usage: graph-engine autopilot merge-up <runId> <ticketId>"
	autopilotContextUsage      = "usage: graph-engine autopilot worker-context <runId> <ticketId>"
	autopilotRecordUsage       = "usage: graph-engine autopilot record-decision <runId> <ticketId> <kind> <content|->"
	autopilotAttachUsage       = "usage: graph-engine autopilot attach-decisions <runId> <ticketId> <nodeId>"
	autopilotTouchUsage        = "usage: graph-engine autopilot touch <runId> <ticketId> [--awaiting-human <what>]"
	autopilotReportUsage       = "usage: graph-engine autopilot report <runId> <ticketId> --result done|failed|blocked [--reason <code>] --summary <text|->"
	autopilotMergeParentUsage  = "usage: graph-engine autopilot merge-into-parent <runId> <ticketId>"
	autopilotSummaryUsage      = "usage: graph-engine autopilot summary <runId>"
	autopilotStatusUsage       = "usage: graph-engine autopilot status [--project <id>]"
)

// autopilotWaitDefaultTimeout is `autopilot wait`'s default --timeout (D4-2).
const autopilotWaitDefaultTimeout = 10 * time.Minute

// newAutopilotService builds the runner the run subcommands use. A package
// variable so tests can inject a fake launcher, clock and poll interval.
var newAutopilotService = func(repo store.GraphRepository, rc runtimeConfig) *runner.Service {
	return runner.New(runner.Options{
		Repo: repo, HomeDir: rc.HomeDir,
		UserExtensionsDir: rc.UserExtensionsDir, TeamExtensionsDir: rc.TeamExtensionsDir,
		TerminalCommand: rc.TerminalCommand, ClaudeBinary: rc.ClaudeBinary,
		// The registry's operational warnings go to stderr, out of the
		// one-line JSON the orchestrator reads on stdout.
		Logf: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "graph-engine: warning: "+format+"\n", args...)
		},
	})
}

// autopilotStdin is where "-" arguments are read from; a variable for tests.
var autopilotStdin io.Reader = os.Stdin

// cmdAutopilot dispatches `graph-engine autopilot <sub>` (DFLT-00142).
//
// Every run subcommand prints one small JSON line (summary prints Markdown),
// because the orchestrator session reads all of it: keeping each answer to a
// few hundred bytes is what lets one orchestrator drive a 20-ticket tree
// (completion criterion 4).
func cmdAutopilot(repo store.GraphRepository, rc runtimeConfig, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", autopilotUsageLine)
	}
	sub, rest := args[0], args[1:]
	if sub == "settings" {
		return cmdAutopilotSettings(repo, rc, rest)
	}
	svc := newAutopilotService(repo, rc)
	switch sub {
	case "start":
		return cmdAutopilotStart(svc, rest)
	case "next":
		pos, err := autopilotPositional(rest, 1, autopilotNextUsage)
		if err != nil {
			return err
		}
		res, err := svc.Next(pos[0])
		if err != nil {
			return err
		}
		return printCompactJSON(res)
	case "launch":
		return cmdAutopilotLaunch(svc, rest)
	case "wait":
		return cmdAutopilotWait(svc, rest)
	case "merge-up":
		pos, err := autopilotPositional(rest, 2, autopilotMergeUpUsage)
		if err != nil {
			return err
		}
		res, err := svc.MergeUp(pos[0], pos[1])
		if err != nil {
			return err
		}
		return printCompactJSON(res)
	case "worker-context":
		pos, err := autopilotPositional(rest, 2, autopilotContextUsage)
		if err != nil {
			return err
		}
		res, err := svc.WorkerContext(pos[0], pos[1])
		if err != nil {
			return err
		}
		return printCompactJSON(res)
	case "record-decision":
		pos, err := autopilotPositional(rest, 4, autopilotRecordUsage)
		if err != nil {
			return err
		}
		content, err := autopilotTextArg(pos[3])
		if err != nil {
			return err
		}
		if err := svc.RecordDecision(pos[0], pos[1], pos[2], content); err != nil {
			return err
		}
		return printCompactJSON(map[string]any{"recorded": pos[2], "ticket": pos[1]})
	case "attach-decisions":
		pos, err := autopilotPositional(rest, 3, autopilotAttachUsage)
		if err != nil {
			return err
		}
		res, err := svc.AttachDecisions(pos[0], pos[1], pos[2])
		if err != nil {
			return err
		}
		return printCompactJSON(res)
	case "touch":
		return cmdAutopilotTouch(svc, rest)
	case "report":
		return cmdAutopilotReport(svc, rest)
	case "merge-into-parent":
		pos, err := autopilotPositional(rest, 2, autopilotMergeParentUsage)
		if err != nil {
			return err
		}
		res, err := svc.MergeIntoParent(pos[0], pos[1])
		if err != nil {
			return err
		}
		return printCompactJSON(res)
	case "summary":
		pos, err := autopilotPositional(rest, 1, autopilotSummaryUsage)
		if err != nil {
			return err
		}
		res, err := svc.Summary(pos[0])
		if err != nil {
			return err
		}
		fmt.Print(res.Markdown)
		return nil
	case "status":
		return cmdAutopilotStatus(repo, rc, svc, rest)
	default:
		return fmt.Errorf("unknown autopilot subcommand %q; %s", sub, autopilotUsageLine)
	}
}

// printCompactJSON prints v as one line of JSON.
func printCompactJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Println(string(data))
	return err
}

// autopilotPositional checks that args are exactly n positional arguments.
func autopilotPositional(args []string, n int, usage string) ([]string, error) {
	for _, a := range args {
		if strings.HasPrefix(a, "--") {
			return nil, fmt.Errorf("%s: unexpected argument %q", usage, a)
		}
	}
	if len(args) != n {
		return nil, fmt.Errorf("%s", usage)
	}
	for _, a := range args {
		if a == "" {
			return nil, fmt.Errorf("%s: arguments must not be empty", usage)
		}
	}
	return args, nil
}

// autopilotFlags splits args into positional arguments and --flag values for
// the given flags (each taking one value, at most once).
func autopilotFlags(args []string, usage string, flags ...string) (pos []string, vals map[string]string, err error) {
	known := map[string]bool{}
	for _, f := range flags {
		known[f] = true
	}
	vals = map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--") {
			if !known[a] {
				return nil, nil, fmt.Errorf("%s: unexpected argument %q", usage, a)
			}
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("%s: %s needs a value", usage, a)
			}
			if _, dup := vals[a]; dup {
				return nil, nil, fmt.Errorf("%s: %s given more than once", usage, a)
			}
			vals[a] = args[i+1]
			i++
			continue
		}
		pos = append(pos, a)
	}
	return pos, vals, nil
}

// autopilotTextArg resolves a <text|-> argument: "-" reads stdin.
func autopilotTextArg(arg string) (string, error) {
	if arg != "-" {
		return arg, nil
	}
	data, err := io.ReadAll(autopilotStdin)
	if err != nil {
		return "", fmt.Errorf("reading stdin: %w", err)
	}
	return string(data), nil
}

func cmdAutopilotStart(svc *runner.Service, args []string) error {
	pos, vals, err := autopilotFlags(args, autopilotStartUsage, "--mode", "--run")
	if err != nil {
		return err
	}
	if len(pos) != 1 || pos[0] == "" {
		return fmt.Errorf("%s", autopilotStartUsage)
	}
	mode, ok := vals["--mode"]
	if !ok {
		return fmt.Errorf("%s: --mode is required", autopilotStartUsage)
	}
	if mode != autopilot.ModeTicket && mode != autopilot.ModeTree {
		return fmt.Errorf("%s: --mode must be ticket or tree, got %q", autopilotStartUsage, mode)
	}
	res, err := svc.Start(pos[0], mode, vals["--run"], false)
	if err != nil {
		return err
	}
	return printCompactJSON(res)
}

func cmdAutopilotLaunch(svc *runner.Service, args []string) error {
	pos, vals, err := autopilotFlags(args, autopilotLaunchUsage, "--role")
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return fmt.Errorf("%s", autopilotLaunchUsage)
	}
	res, err := svc.Launch(pos[0], pos[1], vals["--role"])
	if err != nil {
		return err
	}
	return printCompactJSON(res)
}

// cmdAutopilotWait follows wait-node's exit code convention: 0 when the
// session reported (or was failed as unresponsive), 2 on timeout, 1 on error.
func cmdAutopilotWait(svc *runner.Service, args []string) error {
	pos, vals, err := autopilotFlags(args, autopilotWaitUsage, "--timeout")
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return fmt.Errorf("%s", autopilotWaitUsage)
	}
	timeout := autopilotWaitDefaultTimeout
	if v, ok := vals["--timeout"]; ok {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return fmt.Errorf("%s: invalid --timeout %q (want a positive Go duration such as 30s, 10m)", autopilotWaitUsage, v)
		}
		timeout = d
	}
	res, err := svc.Wait(pos[0], pos[1], timeout)
	if err != nil {
		return err
	}
	if err := printCompactJSON(res); err != nil {
		return err
	}
	if !res.Reported() {
		return exitCodeError{code: waitNodeTimeoutExitCode}
	}
	return nil
}

func cmdAutopilotTouch(svc *runner.Service, args []string) error {
	pos, vals, err := autopilotFlags(args, autopilotTouchUsage, "--awaiting-human")
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return fmt.Errorf("%s", autopilotTouchUsage)
	}
	awaiting, has := vals["--awaiting-human"]
	if has && strings.TrimSpace(awaiting) == "" {
		return fmt.Errorf("%s: --awaiting-human needs a description of what is awaited", autopilotTouchUsage)
	}
	if err := svc.Touch(pos[0], pos[1], awaiting); err != nil {
		return err
	}
	out := map[string]any{"touched": pos[1]}
	if has {
		out["awaiting_human"] = strings.TrimSpace(awaiting)
	}
	return printCompactJSON(out)
}

func cmdAutopilotReport(svc *runner.Service, args []string) error {
	pos, vals, err := autopilotFlags(args, autopilotReportUsage, "--result", "--reason", "--summary")
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return fmt.Errorf("%s", autopilotReportUsage)
	}
	result, ok := vals["--result"]
	if !ok {
		return fmt.Errorf("%s: --result is required", autopilotReportUsage)
	}
	summaryArg, ok := vals["--summary"]
	if !ok {
		return fmt.Errorf("%s: --summary is required", autopilotReportUsage)
	}
	summary, err := autopilotTextArg(summaryArg)
	if err != nil {
		return err
	}
	res, err := svc.Report(pos[0], pos[1], result, vals["--reason"], summary)
	if err != nil {
		return err
	}
	return printCompactJSON(res)
}

func cmdAutopilotStatus(repo store.GraphRepository, rc runtimeConfig, svc *runner.Service, args []string) error {
	pos, vals, err := autopilotFlags(args, autopilotStatusUsage, "--project")
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return fmt.Errorf("%s", autopilotStatusUsage)
	}
	projectID, notice, err := resolveCLIProject(repo, rc, vals["--project"])
	if err != nil {
		return err
	}
	runs, err := svc.Status(projectID)
	if err != nil {
		return err
	}
	if err := printJSON(map[string]any{"project_id": projectID, "runs": runs}); err != nil {
		return err
	}
	printResolvedProjectNotice(notice)
	return nil
}

// cmdAutopilotSettings prints the project's effective autopilot settings as
// JSON -- the same shape as GET /api/projects/{id}/autopilot-settings: the
// values in effect, each key's source, locked state and local/team/default
// values, and warnings. It is read-only on purpose: the HTTP API is the one
// write path, so there is exactly one place that validates a save. The
// project is resolved like list-labels' (--project, else cwd's local path,
// else the current project).
func cmdAutopilotSettings(repo store.GraphRepository, rc runtimeConfig, args []string) error {
	pos, vals, err := autopilotFlags(args, autopilotSettingsUsageLine, "--project")
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return fmt.Errorf("%s: unexpected argument %q", autopilotSettingsUsageLine, pos[0])
	}
	if v, ok := vals["--project"]; ok && v == "" {
		return fmt.Errorf("%s: --project needs a value", autopilotSettingsUsageLine)
	}
	projectID, notice, err := resolveCLIProject(repo, rc, vals["--project"])
	if err != nil {
		return err
	}
	project, err := repo.GetProject(projectID)
	if err != nil {
		return err
	}
	if project == nil {
		return domain.NewAPIError(domain.ErrCodeProjectNotFound, "PROJECT_NOT_FOUND: project not found: %s", projectID)
	}
	// A home config that cannot be parsed reads as "no local values", like
	// every other reader; loadRuntimeConfig has already warned about it.
	fileCfg, _ := runtimeconfig.LoadHomeConfig(rc.HomeDir)
	roots := config.ResolveRoots(rc.UserExtensionsDir, rc.TeamExtensionsDir)
	eff := autopilot.ResolveProject(projectID, fileCfg.AutopilotLocal(projectID), roots.TeamDir)
	if err := printJSON(eff); err != nil {
		return err
	}
	printResolvedProjectNotice(notice)
	return nil
}
