// Command graph-engine is the single cross-platform binary that replaces the
// old Node.js CLI (cli.js) and server (server.ts): it bundles every
// subcommand the plugin shells out to, plus `serve` for the REST+SSE API the
// web UI talks to.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/graph-ops/core-go/internal/artifactcontent"
	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/httpserver"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
	"github.com/graph-ops/core-go/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}
	// A help request is answered before any command runs, so asking for usage
	// can never write anything. Without this, "create-ticket --help" took
	// "--help" as the title and silently created a ticket.
	if isHelpRequest(os.Args[1:]) {
		printUsage()
		return
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		// Most errors exit 1 with "Error: ..." on stderr. An exitCodeError
		// (wait-node's timeout) exits with its own code and prints nothing
		// extra, since the command already wrote its result to stdout.
		code, printErr := exitCodeFor(err)
		if printErr {
			fmt.Fprintln(os.Stderr, "Error:", err)
		}
		os.Exit(code)
	}
}

func run(cmd string, args []string) error {
	rc, err := loadRuntimeConfig()
	if err != nil {
		return err
	}

	// Commands that read nothing but configuration are dispatched before the
	// store is opened, so they neither create nor touch a DB file.
	switch cmd {
	case "serve":
		return cmdServe(rc, args)
	case "get-workflow-catalog":
		return cmdGetWorkflowCatalog(rc, args)
	case "get-skill-context":
		return cmdGetSkillContext(rc, args)
	case "get-node-type-context":
		return cmdGetNodeTypeContext(rc, args)
	case "get-report-template":
		return cmdGetReportTemplate(rc)
	case "get-plan-template":
		return cmdGetPlanTemplate(rc)
	case "get-review-template":
		return cmdGetReviewTemplate(rc)
	case "get-extension-roots":
		return cmdGetExtensionRoots(rc)
	}

	repo, err := openStore(rc)
	if err != nil {
		return err
	}
	eng := engine.New(repo)

	switch cmd {
	case "create-ticket":
		return cmdCreateTicket(eng, repo, rc, args)
	case "create-project":
		return cmdCreateProject(repo, rc, args)
	case "list-projects":
		return cmdListProjects(repo, rc)
	case "use-project":
		return cmdUseProject(repo, args)
	case "refine-ticket":
		return cmdRefineTicket(eng, args)
	case "close-ticket":
		return cmdCloseTicket(eng, args)
	case "reopen-ticket":
		return cmdReopenTicket(eng, args)
	case "get-ticket":
		return cmdGetTicket(repo, args)
	case "list-tickets":
		return cmdListTickets(repo)
	case "get-executable":
		return cmdGetExecutable(eng, repo, rc, args)
	case "expand-graph":
		return cmdExpandGraph(eng, repo, rc, args)
	case "complete-node":
		return cmdCompleteNode(eng, repo, args)
	case "reopen-nodes":
		return cmdReopenNodes(eng, args)
	case "unstick-node":
		return cmdUnstickNode(eng, args)
	case "add-artifact":
		return cmdAddArtifact(repo, rc.ArtifactsDir, args)
	case "get-review-criteria":
		return cmdGetReviewCriteria(repo, args)
	case "wait-node":
		return cmdWaitNode(repo, args)
	case "get-language-settings":
		return cmdGetLanguageSettings(repo, rc, args)
	case "ui":
		return cmdUI(rc, args)
	default:
		printUsage()
		os.Exit(1)
		return nil
	}
}

// openStore is store.Open's one call site for both `serve` (cmdServe) and
// every DB-opening CLI subcommand (run, above) -- the two places
// DFLT-00037's plan approval gate flagged (M-1) as needing the same
// mysql-specific hint, since this ticket's default TLS mode change
// (verify-full) can turn an existing MySQL configuration that used to start
// fine into one that fails here, on every single invocation, not just
// `serve`. A hard failure at this ticket's default is intentional (see
// store.Config's and NormalizeMySQLTLSMode's doc comments for why there is
// no plaintext fallback to silently degrade to instead) -- what this
// wrapper adds is only making the failure actionable: which file or env
// vars to look at, since neither `serve` nor a plain CLI subcommand has a
// running settings UI to fix this from once opening the store itself is
// what is failing.
func openStore(rc runtimeConfig) (store.GraphRepository, error) {
	repo, err := store.Open(storeConfigFromRuntimeConfig(rc))
	if err != nil && rc.DBBackend == "mysql" {
		configPath := runtimeconfig.ResolvePath(rc.WorkDir, rc.HomeDir)
		return nil, fmt.Errorf(
			"%w\n\nThis MySQL connection could not be established with its configured TLS settings "+
				"(mysqlTls %q). There is no plaintext fallback -- see this ticket's (DFLT-00037) README "+
				"section on MySQL TLS for why. To recover:\n"+
				"  - If the server does not support TLS, set mysqlTls to \"disabled\" explicitly.\n"+
				"  - If the server uses a self-signed or auto-generated certificate (e.g. a fresh MySQL "+
				"8 install), set mysqlTls to \"verify-ca\" and mysqlTlsCa to that CA's PEM file.\n"+
				"Edit %s directly, or set GRAPH_MYSQL_TLS / GRAPH_MYSQL_TLS_CA -- the web settings UI "+
				"cannot help here, because opening the store is exactly the step that is failing.",
			err, rc.MySQLTLSMode, configPath)
	}
	return repo, err
}

// isHelpRequest reports whether args (the arguments after the binary name) ask
// for usage rather than for work: either the command itself is a help word, or
// any argument to it is an explicit help flag. Checking every argument -- not
// just the first -- is what makes "create-ticket --help" safe, since a command
// that takes free text would otherwise accept the flag as that text.
//
// The cost is that a title, description or artifact whose whole value is
// literally "--help" or "-h" cannot be passed on the command line. Those go
// through stdin ("-") or the HTTP API instead.
func isHelpRequest(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "help", "--help", "-h":
		return true
	}
	for _, a := range args[1:] {
		if a == "--help" || a == "-h" {
			return true
		}
	}
	return false
}

func printUsage() {
	fmt.Println(`Graph Engine CLI

Commands:
  create-project <name> [--prefix P] [--workdir path]
                                          (--prefix omitted -> derived from name and de-duplicated;
                                           --workdir is the project's local path in this environment,
                                           saved to graph-config.json's projectPaths (not the DB);
                                           omitted -> cwd, relative -> resolved against the cwd.
                                           Does not switch the current project; follow up with use-project)
  list-projects                           (each project with this environment's "local_path", "" if unset)
  use-project <projectId>                (sets the current project; POST /api/tickets and GET /api/tickets
                                           default to whichever project is current. create-ticket uses it
                                           only as a fallback when the cwd matches no project's local path)
  create-ticket <title> [description] [--project <id>] [--priority <HIGH|MEDIUM|LOW>]
                                          (--project omitted -> the project whose local path (projectPaths in
                                           graph-config.json) is the cwd or contains it (deepest nested
                                           local path wins), else the current
                                           project, else an error. Paths are compared as written, so a
                                           symlinked or case-differing cwd does not match and falls back.
                                           The choice is reported as one stderr line, "resolved project:
                                           <name> (<id>) from current directory" or "... from current
                                           project (use-project)"; stdout stays the ticket JSON only.
                                           --project given -> used as-is, nothing on stderr.
                                           --priority omitted -> created with no priority set; given ->
                                           validated (HIGH/MEDIUM/LOW only) before the ticket is created.
                                           Tickets start unassigned; use the Web UI's assign button)
  refine-ticket <ticketId> [description|-] [--priority <HIGH|MEDIUM|LOW|none>]
                                          (replaces the ticket's description with the refined text; builds
                                           no graph. --priority is independent of the description: omitted
                                           leaves the stored priority untouched, "none" clears it back to
                                           unset, HIGH/MEDIUM/LOW sets it -- so "refine-ticket <id>
                                           --priority LOW" with no description positional changes only the
                                           priority)
  close-ticket <ticketId> [--reason "<text>"]
                                          (withdraws the ticket without marking it complete: sets status to
                                           CLOSED from ANY status, including one with nodes IN PROGRESS/IN
                                           REVIEW -- node state is left untouched. --reason (optional) is
                                           saved as the ticket's closed_reason, overwriting whatever a
                                           previous close left; visible via get-ticket and the Web UI. Once
                                           CLOSED, syncTicketStatus (driven by complete-node etc.) never
                                           changes the status again, and get-executable returns no nodes
                                           and creates none, until reopen-ticket is called.)
  reopen-ticket <ticketId>               (moves a CLOSED ticket back into the normal status flow, deriving
                                           the new status from its nodes exactly like complete-node's own
                                           status sync would (DONE/IN RELEASE/IN REVIEW/IN PROGRESS); if
                                           that yields nothing -- no nodes, or all still TODO -- falls back
                                           to REFINED if the ticket has ever been refined, else TODO. Errors
                                           if the ticket is not currently CLOSED.)
  get-ticket <ticketId>
  list-tickets
  get-executable <ticketId> [--language <code>]
                                          (auto-seeds the graph's plan/plan_review nodes on first call;
                                           --language, only meaningful on that first/seeding call, is this
                                           one call's explicit language choice -- see get-language-settings --
                                           and outranks any persistent team/user language setting)
  expand-graph <ticketId> [--patch <file|->] [--language <code>]
                                          (call once the seed passes; no patch = default full template;
                                           --language is this one call's explicit language choice, same
                                           precedence note as get-executable's)
  complete-node <nodeId> [passed:true|false] [--reason "<text>"]
                                          (--reason saves the text as a "rejection_reason" text
                                           artifact on the node in the same call; valid ONLY when
                                           passed=false and the node is type approval_gate --
                                           any other combination (passed=true, or a non-approval_gate
                                           node) is rejected as a misuse and leaves the node/ticket
                                           untouched. reopen-nodes' caller finds the reason back via
                                           this fixed artifact name.)
  reopen-nodes <ticketId> <nodeId1,nodeId2,...>
                                          (mechanical primitive for rejection triage (DFLT-00016):
                                           resets the given already-DONE/REJECTED nodes (chosen by
                                           process-ticket, never by this command) back to TODO, plus
                                           whatever DONE/REJECTED work is reachable from them via a
                                           success edge (forward closure), bumping each one's
                                           iteration_count by 1 and clearing the ticket's blocked
                                           flag. Requires the ticket to currently be blocked. Errors,
                                           writing nothing, if any collected node would exceed its
                                           max_iterations -- no partial application.)
  unstick-node <nodeId>                  (resets a single node stuck at IN PROGRESS/IN REVIEW back to
                                           TODO, no iteration_count change, no Blocked precondition --
                                           for a node get-executable claimed but that no worker ever
                                           actually completed (e.g. a stray get-executable poll from
                                           elsewhere claimed it out from under the intended dispatch).
                                           process-ticket must first satisfy itself nothing is still
                                           working the node -- this does not check.)
  add-artifact <ticketId> <nodeId> <name> <type:text|gherkin|html|image|json> [contentOrPath] [--allow-outside-artifacts-dir]
                                          (for a "report" node's html artifact, contentOrPath's file
                                           must match the fixed report template's structural markers;
                                           for html/image, a contentOrPath that names a real file is read
                                           into the DB. The file must resolve inside the configured
                                           artifacts directory (graph-config.json's artifactsDir /
                                           $GRAPH_ARTIFACTS_DIR) unless --allow-outside-artifacts-dir is
                                           passed explicitly; image content must also start with a
                                           recognized image file signature.
                                           text/gherkin/json NEVER read a file path -- contentOrPath is
                                           always taken as the literal content for those types, matching
                                           the DB-only storage contract (DFLT-00006). Pass "-" instead to
                                           read the content from stdin, e.g. for a large write-up:
                                             cat notes.md | graph-engine add-artifact T N Name text -)
  get-review-criteria <nodeId>
  wait-node <nodeId> [<nodeId> ...] [--timeout <duration>]
                                          (blocks, polling the DB every ~2s, until at least one given node
                                           is no longer TODO -- any other status counts, including IN
                                           PROGRESS, not only DONE/REJECTED -- then prints
                                           {"result":"changed","nodes":[{"id","status","rejection_reason"}]}
                                           listing every node changed at that moment (rejection_reason only
                                           for REJECTED, from the newest rejection_reason artifact) and
                                           exits 0. --timeout takes a Go duration (30s, 10m, 12h); omitted ->
                                           waits forever. On timeout prints {"result":"timeout","nodes":[]}
                                           and exits 2. An unknown node id is an error (exit 1) before any
                                           waiting starts. process-ticket runs this in the background at an
                                           approval_gate so a Web UI approve/reject resumes the session.)
  ui                                      (opens the local Web UI, in the default browser, on the project
                                           whose local path (projectPaths in graph-config.json) is the current
                                           directory or contains it (deepest wins) -- auto-starting the UI
                                           server first if it isn't already running. If no project matches,
                                           opens the Web UI's project-setup dialog for the current directory
                                           instead, to create a new project or pick an existing one. Fails only if starting the UI server itself
                                           fails; a browser-launch failure is a warning with the URL printed
                                           for you to open by hand.)
  get-workflow-catalog [--language <code>]
                                          (plugin default -> user -> team workflow.yaml/config.yaml merge;
                                           --language previews the merge as if it resolved to that code,
                                           same precedence note as get-executable's)
  get-skill-context <skill-name>         (merged user/team extension text for a plugin skill)
  get-node-type-context <node-type>      (merged plugin-default + user + team agent instructions for a node type)
  get-report-template                    (resolved fixed HTML report template: team -> user -> plugin default)
  get-plan-template                      (resolved fixed Markdown plan template: team -> user -> plugin default)
  get-review-template                    (resolved fixed Markdown review template, shared by the review/
                                           review_gate node types: team -> user -> plugin default)
  get-extension-roots                    ({"userDir","teamDir"} resolved paths, for a skill that needs to
                                           write into that tree directly, e.g. onboarding's language setup)
  get-language-settings [--project <id>] ({"resolved","source":"team"|"user"|"none","supported_locales"} --
                                           whether a persistent language setting exists (team tier, else user
                                           tier) and what it resolves to, so onboarding/process-ticket can
                                           tell that apart from "nothing set yet, decide one for this
                                           session" without parsing prose. --project resolves the team tier
                                           from that project's local path, same as get-executable/
                                           expand-graph's ticket-based resolution; omitted, or no local
                                           path set, falls back to this process's own team root)
  serve [--port N] [--host ADDR]
                                          (--host omitted -> GRAPH_HOST / graph-config.json's "host" /
                                           127.0.0.1. This API has no authentication, so it listens on
                                           loopback only unless you deliberately widen it, e.g.
                                           --host 0.0.0.0 on a network you trust.)

User/team extension directories (see README.md "Configuration" section):
  GRAPH_USER_EXTENSIONS_DIR / userExtensionsDir   (default: $HOME/.graph-ops)
  GRAPH_TEAM_EXTENSIONS_DIR / teamExtensionsDir   (default: nearest ancestor .graph-ops/)

Help:
  graph-engine help | --help | -h         (prints this list)
  Passing --help or -h to any command prints this list instead of running it,
  so no command can mistake a help request for a title, description or id.`)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// cmdCreateTicket parses positional args (title, [description]) and optional
// --project <id> / --priority <HIGH|MEDIUM|LOW> flags interspersed anywhere
// among them.
//
// An explicit --project always wins; nothing else is consulted and nothing
// is written to stderr. With no --project, the target is resolved by
// resolveCreateTicketProject: the project whose local path (rc.ProjectPaths)
// contains the CLI's cwd (rc.WorkDir, deepest match), else the currently-selected project
// (use-project / the Web UI's switcher), else an error. The chosen project
// and how it was chosen are then reported as one line on stderr, keeping
// stdout the ticket JSON alone.
//
// This deliberately differs from POST /api/tickets (handleCreateTicket),
// which resolves only via the current project: an HTTP request carries no
// working directory, whereas the CLI always runs somewhere, and the global
// current project can be switched from the Web UI at any time independently
// of where the CLI is invoked (DFLT-00025). Do not "fix" one to match the
// other.
//
// --priority (DFLT-00059) is validated with domain.ParseTicketPriority
// before anything is written, same as the HTTP API's create/update paths --
// an invalid value fails the whole command rather than creating the ticket
// with priority silently dropped. Omitting --priority creates the ticket
// with no priority set, exactly like before this flag existed.
//
// More than two positionals is a usage error rather than silently dropping
// the extras: the third positional used to be the (since removed) assignee,
// so ignoring it would let an old invocation "succeed" while leaving the user
// believing the ticket had been assigned. This deliberately differs from the
// API, which ignores an unknown "assignee" key -- for the CLI the positional
// count itself is the contract.
func cmdCreateTicket(eng *engine.GraphEngine, repo store.GraphRepository, rc runtimeConfig, args []string) error {
	const usage = `usage: graph-engine create-ticket <title> [description] [--project <id>] [--priority <HIGH|MEDIUM|LOW>]`

	var projectFlag, priorityFlag string
	var positional []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--project" && i+1 < len(args):
			projectFlag = args[i+1]
			i++
		case args[i] == "--priority" && i+1 < len(args):
			priorityFlag = args[i+1]
			i++
		default:
			positional = append(positional, args[i])
		}
	}
	if len(positional) < 1 || len(positional) > 2 {
		return fmt.Errorf(usage)
	}
	title := positional[0]
	description := ""
	if len(positional) > 1 {
		description = positional[1]
	}

	var priority *domain.TicketPriority
	if priorityFlag != "" {
		parsed, err := domain.ParseTicketPriority(priorityFlag)
		if err != nil {
			return fmt.Errorf("%s: %w", usage, err)
		}
		priority = &parsed
	}

	if projectFlag != "" {
		ticket, err := eng.CreateTicketWithPriority(projectFlag, title, description, priority)
		if err != nil {
			return err
		}
		return printJSON(ticket)
	}

	project, source, err := resolveCreateTicketProject(repo, rc.WorkDir, rc.ProjectPaths)
	if err != nil {
		return err
	}
	ticket, err := eng.CreateTicketWithPriority(project.ID, title, description, priority)
	if err != nil {
		return err
	}
	if err := printJSON(ticket); err != nil {
		return err
	}
	// stdout stays the ticket JSON alone (callers parse it); the implicit
	// choice goes to stderr so a human can spot a surprising target.
	fmt.Fprintf(os.Stderr, "resolved project: %s (%s) from %s\n", project.Name, project.ID, source)
	return nil
}

// cmdCreateProject creates a Project. It deliberately does not switch the
// current project (see use-project) -- creating and selecting are separate,
// explicit steps for the CLI, mirroring the Web UI's create-then-switch flow
// (see the execution plan section 7.3).
//
// --workdir (default: the cwd) is the project's local path in this
// environment. Since DFLT-00080 it is not stored in the DB -- which may be
// shared by a whole team -- but in graph-config.json's projectPaths, through
// runtimeconfig.SetProjectPath (the same file loadRuntimeConfig read). A
// relative --workdir is resolved against rc.WorkDir (the cwd).
func cmdCreateProject(repo store.GraphRepository, rc runtimeConfig, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf(`usage: graph-engine create-project <name> [--prefix P] [--workdir path]`)
	}
	name := args[0]
	var prefix, workdir string
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--prefix":
			if i+1 < len(args) {
				prefix = args[i+1]
				i++
			}
		case "--workdir":
			if i+1 < len(args) {
				workdir = args[i+1]
				i++
			}
		}
	}
	base := rc.WorkDir
	if base == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		base = cwd
	}
	localPath := workdir
	if localPath == "" {
		localPath = base
	} else if !filepath.IsAbs(localPath) {
		localPath = filepath.Join(base, localPath)
	}
	localPath = filepath.Clean(localPath)

	project, err := repo.CreateProject(name, prefix)
	if err != nil {
		return err
	}
	if _, err := runtimeconfig.SetProjectPath(rc.WorkDir, rc.HomeDir, project.ID, localPath); err != nil {
		return fmt.Errorf("project %s (%s) was created, but saving its local path to graph-config.json failed: %w", project.Name, project.ID, err)
	}
	return printJSON(cliProject{Project: project, LocalPath: localPath})
}

// cliProject is list-projects/create-project's output shape: the DB project
// plus this environment's local path ("" when unset), matching the Web API's
// project responses (internal/httpserver's projectResponse).
type cliProject struct {
	domain.Project
	LocalPath string `json:"local_path"`
}

func cmdListProjects(repo store.GraphRepository, rc runtimeConfig) error {
	projects, err := repo.ListProjects()
	if err != nil {
		return err
	}
	fileCfg := runtimeconfig.FileConfig{ProjectPaths: rc.ProjectPaths}
	out := make([]cliProject, 0, len(projects))
	for _, p := range projects {
		out = append(out, cliProject{Project: p, LocalPath: fileCfg.ProjectPath(p.ID)})
	}
	return printJSON(out)
}

func cmdUseProject(repo store.GraphRepository, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: graph-engine use-project <projectId>")
	}
	project, err := repo.GetProject(args[0])
	if err != nil {
		return err
	}
	if project == nil {
		return fmt.Errorf("project %s not found", args[0])
	}
	if err := repo.SetCurrentProjectID(project.ID); err != nil {
		return err
	}
	return printJSON(project)
}

// cmdRefineTicket no longer builds any graph -- it just replaces the
// ticket's description with the refined text (completion criteria /
// rationale ("why"), folded together with whatever from the original still
// applies). Graph construction now happens lazily from process-ticket (see
// cmdGetExecutable, which drives engine.EnsureGraphStarted, and
// cmdExpandGraph, which the skill calls explicitly once the seed passes).
//
// --priority (DFLT-00059) is parsed independently of the [description|-]
// positional, and interspersed the same way create-ticket's --project/
// --priority flags are: omitted -> engine.NoPriorityChange() (leave the
// stored priority untouched, the same behavior as before this flag
// existed); "none" -> engine.ClearPriority() (reset to unset); any other
// value is validated with domain.ParseTicketPriority and, if valid, becomes
// engine.SetPriority(...). This lets a caller change only the priority
// (`refine-ticket <id> --priority LOW`, description omitted so it too is
// left unchanged) as easily as only the description.
func cmdRefineTicket(eng *engine.GraphEngine, args []string) error {
	const usage = `usage: graph-engine refine-ticket <ticketId> [description|-] [--priority <HIGH|MEDIUM|LOW|none>]`
	if len(args) < 1 {
		return fmt.Errorf(usage)
	}
	ticketID := args[0]

	var priorityFlag string
	var priorityGiven bool
	var positional []string
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--priority" {
			if i+1 >= len(rest) {
				return fmt.Errorf("%s: --priority requires a value", usage)
			}
			priorityFlag = rest[i+1]
			priorityGiven = true
			i++
			continue
		}
		positional = append(positional, rest[i])
	}
	if len(positional) > 1 {
		return fmt.Errorf(usage)
	}

	description := ""
	if len(positional) > 0 {
		if positional[0] == "-" {
			raw, err := io.ReadAll(os.Stdin)
			if err != nil {
				return fmt.Errorf("reading description from stdin: %w", err)
			}
			description = string(raw)
		} else {
			description = positional[0]
		}
	}

	priority := engine.NoPriorityChange()
	if priorityGiven {
		switch priorityFlag {
		case "none":
			priority = engine.ClearPriority()
		default:
			parsed, err := domain.ParseTicketPriority(priorityFlag)
			if err != nil {
				return fmt.Errorf("%s: %w", usage, err)
			}
			priority = engine.SetPriority(parsed)
		}
	}

	ticket, err := eng.RefineTicket(ticketID, description, priority)
	if err != nil {
		return err
	}
	return printJSON(ticket)
}

// cmdCloseTicket parses the ticketId positional plus an optional --reason
// flag (DFLT-00043) and calls engine.CloseTicket. Unlike complete-node's
// --reason (valid only for a rejected approval_gate), this --reason is always
// optional and always accepted, since closing works from any ticket status.
func cmdCloseTicket(eng *engine.GraphEngine, args []string) error {
	const usage = `usage: graph-engine close-ticket <ticketId> [--reason "<text>"]`
	if len(args) < 1 {
		return fmt.Errorf(usage)
	}
	ticketID := args[0]
	var reason string
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--reason" {
			if i+1 >= len(rest) {
				return fmt.Errorf("%s: --reason requires a value", usage)
			}
			reason = rest[i+1]
			i++
			continue
		}
		return fmt.Errorf("%s: unrecognized argument %q", usage, rest[i])
	}

	ticket, err := eng.CloseTicket(ticketID, reason)
	if err != nil {
		return err
	}
	return printJSON(ticket)
}

func cmdReopenTicket(eng *engine.GraphEngine, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: graph-engine reopen-ticket <ticketId>")
	}
	ticket, err := eng.ReopenTicket(args[0])
	if err != nil {
		return err
	}
	return printJSON(ticket)
}

func cmdGetTicket(repo store.GraphRepository, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: graph-engine get-ticket <ticketId>")
	}
	detail, err := repo.GetTicketDetail(args[0])
	if err != nil {
		return err
	}
	if detail == nil {
		return fmt.Errorf("ticket %s not found", args[0])
	}
	return printJSON(detail)
}

func cmdListTickets(repo store.GraphRepository) error {
	tickets, err := repo.ListTickets()
	if err != nil {
		return err
	}
	return printJSON(tickets)
}

func cmdGetExecutable(eng *engine.GraphEngine, repo store.GraphRepository, rc runtimeConfig, args []string) error {
	const usage = `usage: graph-engine get-executable <ticketId> [--language <code>]`
	if len(args) < 1 {
		return fmt.Errorf(usage)
	}
	ticketID := args[0]
	var language string
	for i := 1; i < len(args); i++ {
		if args[i] == "--language" && i+1 < len(args) {
			language = args[i+1]
			i++
			continue
		}
		return fmt.Errorf("%s: unrecognized argument %q", usage, args[i])
	}

	catalog, err := catalogForTicket(repo, rc, ticketID, language)
	if err != nil {
		return err
	}
	nodes, err := eng.GetExecutableNodes(ticketID, catalog)
	if err != nil {
		return err
	}
	return printJSON(nodes)
}

// cmdExpandGraph builds the rest of a ticket's graph once the seed
// (plan/plan_review) has passed. Deciding the shape (investigation-only?
// implementation without Gherkin? the standard full flow?) is the calling
// skill's job, expressed as a patch; omitting --patch falls back to the
// catalog's default full template.
func cmdExpandGraph(eng *engine.GraphEngine, repo store.GraphRepository, rc runtimeConfig, args []string) error {
	const usage = `usage: graph-engine expand-graph <ticketId> [--patch <file|->] [--language <code>]`
	if len(args) < 1 {
		return fmt.Errorf(usage)
	}
	ticketID := args[0]

	var patch *engine.Patch
	var language string
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--patch":
			if i+1 < len(args) {
				p, err := readPatch(args[i+1])
				if err != nil {
					return err
				}
				patch = p
				i++
			}
		case "--language":
			if i+1 < len(args) {
				language = args[i+1]
				i++
			}
		}
	}

	catalog, err := catalogForTicket(repo, rc, ticketID, language)
	if err != nil {
		return err
	}
	if err := eng.ExpandGraph(ticketID, catalog, patch); err != nil {
		return err
	}
	detail, err := repo.GetTicketDetail(ticketID)
	if err != nil {
		return err
	}
	return printJSON(detail)
}

func readPatch(source string) (*engine.Patch, error) {
	var raw []byte
	var err error
	if source == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(source)
	}
	if err != nil {
		return nil, fmt.Errorf("reading patch: %w", err)
	}
	var patch engine.Patch
	if err := json.Unmarshal(raw, &patch); err != nil {
		return nil, fmt.Errorf("parsing patch JSON: %w", err)
	}
	return &patch, nil
}

// cmdCompleteNode parses the nodeId/passed positionals plus an optional
// --reason flag (DFLT-00016). --reason is the CLI's way to attach a rejected
// approval_gate's free-text reason in the same call as the rejection itself
// -- the same convention handleCompleteNode's request body already supports
// via an inline "artifacts" array (see engine.CompleteNode's doc comment),
// just spelled as a flag for interactive/scripted CLI use rather than a JSON
// body. It's deliberately restricted to exactly the one case it exists for
// (passed=false against an approval_gate node): --reason on an approval, or
// on any other node type, is rejected outright rather than silently
// accepted-and-ignored or attached somewhere a later reader wouldn't expect
// it -- see plan art-5f8847a4 section 2.3(a) and the corresponding
// misuse-prevention scenarios in the Gherkin spec (art-eff6ffdb section 3.5).
func cmdCompleteNode(eng *engine.GraphEngine, repo store.GraphRepository, args []string) error {
	const usage = `usage: graph-engine complete-node <nodeId> [passed:true|false] [--reason "<text>"]`
	if len(args) < 1 {
		return fmt.Errorf(usage)
	}
	nodeID := args[0]
	passed := true
	var reason string
	var haveReason bool
	rest := args[1:]
	// The optional bare "passed" positional, if present, is always
	// immediately after nodeId and never itself starts with "--".
	if len(rest) > 0 && rest[0] != "--reason" {
		passed = rest[0] != "false"
		rest = rest[1:]
	}
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--reason" {
			if i+1 >= len(rest) {
				return fmt.Errorf("%s: --reason requires a value", usage)
			}
			reason = rest[i+1]
			haveReason = true
			i++
			continue
		}
		return fmt.Errorf("%s: unrecognized argument %q", usage, rest[i])
	}

	var artifacts []domain.Artifact
	if haveReason {
		node, err := repo.GetNode(nodeID)
		if err != nil {
			return err
		}
		if node == nil {
			return fmt.Errorf("node %s not found", nodeID)
		}
		if passed || node.Type != domain.NodeTypeApprovalGate {
			return fmt.Errorf("--reason is only valid when rejecting (passed=false) an approval_gate node; node %s is type %s with passed=%v", nodeID, node.Type, passed)
		}
		artifacts = []domain.Artifact{{Name: "rejection_reason", Type: domain.ArtifactText, Content: &reason}}
	}

	result, err := eng.CompleteNode(nodeID, passed, artifacts)
	if err != nil {
		return err
	}
	return printJSON(result)
}

// cmdReopenNodes is the CLI-only entry point for engine.ReopenNodes (see its
// doc comment for the full contract). CLI-only, no HTTP counterpart, is
// deliberate: the ticket this implements requires that WHICH nodes to reopen
// is decided by process-ticket's judgment, never chosen by a human clicking
// through the Web UI (see plan art-5f8847a4 section 2.3(c)) -- the same
// reasoning that keeps expand-graph's --patch CLI-only.
func cmdReopenNodes(eng *engine.GraphEngine, args []string) error {
	const usage = `usage: graph-engine reopen-nodes <ticketId> <nodeId1,nodeId2,...>`
	if len(args) < 2 {
		return fmt.Errorf(usage)
	}
	var nodeIDs []string
	for _, raw := range strings.Split(args[1], ",") {
		id := strings.TrimSpace(raw)
		if id != "" {
			nodeIDs = append(nodeIDs, id)
		}
	}
	if len(nodeIDs) == 0 {
		return fmt.Errorf("%s: no node ids given", usage)
	}
	detail, err := eng.ReopenNodes(args[0], nodeIDs)
	if err != nil {
		return err
	}
	return printJSON(detail)
}

// cmdUnstickNode is the CLI-only entry point for engine.UnstickNode (see its
// doc comment for the full contract and the failure mode it recovers from).
// CLI-only for the same reason reopen-nodes and expand-graph's --patch are:
// deciding a node's claim is actually stale is process-ticket's judgment
// call, not something a human should trigger by clicking through the Web UI.
func cmdUnstickNode(eng *engine.GraphEngine, args []string) error {
	const usage = `usage: graph-engine unstick-node <nodeId>`
	if len(args) < 1 {
		return fmt.Errorf(usage)
	}
	node, err := eng.UnstickNode(args[0])
	if err != nil {
		return err
	}
	return printJSON(node)
}

// cmdAddArtifact is the CLI's independent artifact-creation path -- it does
// not call httpserver's prepareArtifactForCreate (the shared choke point
// handleCreateArtifact and handleCompleteNode both now go through, see
// Security review art-cdbe6a11 / 6th security review, DFLT-00006) because
// the two operate under different trust models and inputs that don't map
// onto the same function without either changing CLI behavior or
// reintroducing an HTTP-shaped indirection for no benefit:
//   - This command reads contentOrPath directly off the local filesystem of
//     whichever machine runs it, gated by --allow-outside-artifacts-dir (a
//     deliberate operator opt-out with no HTTP equivalent -- safeArtifactPath
//     never allows escaping ArtifactsDir at all). httpserver's
//     validation/sandbox helpers (safeArtifactPath, prepareArtifactForCreate)
//     are Server methods bound to a fixed ArtifactsDir with no such opt-out,
//     and reshaping them to accept it would just be this same logic moved
//     one file over.
//   - This command has no metadata argument at all (see the positional args
//     above), so the metadata.mime_type-forgery class of bug
//     prepareArtifactForCreate guards against (arts 3d521dc0, 0e621b48)
//     cannot arise here by construction -- there is nothing to reject.
//   - html/image still funnel through artifactcontent.Resolve below, which
//     internally calls the exact same EncodeFile/EncodeInline (and, for
//     images, validateImageBytes' magic-byte check) prepareArtifactForCreate
//     calls -- so the actual encode-and-validate step is shared code, just
//     not the outer HTTP-request-shaped wrapper around it. For non-html/image
//     types, prepareArtifactForCreate's file_path rejection has no CLI
//     counterpart to duplicate: contentOrPath is the single positional
//     argument for both "inline content" and "a file to read", so for those
//     types this command always treats it as literal content (below, the
//     `else { artifact.Content = contentOrPath }` branch) and never even has
//     a FilePath value to reject.
func cmdAddArtifact(repo store.GraphRepository, artifactsDir string, args []string) error {
	// --allow-outside-artifacts-dir can appear anywhere after the required
	// positional args; stripping it out first keeps the positional-argument
	// parsing beneath unaware of flags entirely.
	allowOutside := false
	positional := args[:0:0]
	for _, a := range args {
		if a == "--allow-outside-artifacts-dir" {
			allowOutside = true
			continue
		}
		positional = append(positional, a)
	}
	args = positional

	if len(args) < 4 {
		return fmt.Errorf("usage: graph-engine add-artifact <ticketId> <nodeId> <name> <type> [contentOrPath] [--allow-outside-artifacts-dir]")
	}
	ticketID, nodeID, name, artType := args[0], args[1], args[2], args[3]

	// Verify the ticket/node exist before writing anything.
	ticket, err := repo.GetTicket(ticketID)
	if err != nil {
		return err
	}
	if ticket == nil {
		return fmt.Errorf("ticket %s not found", ticketID)
	}
	node, err := repo.GetNode(nodeID)
	if err != nil {
		return err
	}
	if node == nil {
		return fmt.Errorf("node %s not found", nodeID)
	}
	// The resolved node's own TicketID is authoritative for which ticket the
	// artifact belongs to (same as handleCreateArtifact, which never even
	// takes a separate ticket ID from its request body).
	ticketID, nodeID = node.TicketID, node.ID

	var contentOrPath *string
	if len(args) > 4 {
		contentOrPath = &args[4]
	}
	artifact := domain.Artifact{
		ID: engine.NewArtifactID(), TicketID: ticketID, NodeID: nodeID, Name: name,
		Type: domain.ArtifactType(artType),
	}
	if artType == "image" || artType == "html" {
		// html/image artifacts must carry their actual bytes in the DB, not
		// only a file_path string pointing at whichever machine ran this
		// command -- otherwise a remote/shared DB (DFLT-00006) can't preview
		// them from anywhere else. See artifactcontent.Resolve: when
		// contentOrPath names a real file it is read into Content (and kept
		// as FilePath purely as informational provenance); otherwise
		// contentOrPath is treated as already being the content itself.
		resolved, err := artifactcontent.Resolve(domain.ArtifactType(artType), contentOrPath, artifactcontent.ResolveOptions{
			ArtifactsDir:             artifactsDir,
			AllowOutsideArtifactsDir: allowOutside,
		})
		if err != nil {
			return err
		}
		artifact.Content = resolved.Content
		artifact.Metadata = resolved.Metadata
		artifact.FilePath = resolved.FilePath
	} else {
		// "-" reads the content from stdin instead of taking it as a literal
		// argument, so callers with large or multi-line output (e.g. an
		// investigation write-up) can pipe it in (`cat file.md | ... add-artifact
		// ... text -`) instead of cramming it into a shell argument. This is
		// NOT a file-path read: text/gherkin/json artifacts deliberately never
		// read an arbitrary path off disk (security review art-65fe221a, 5th
		// security review, DFLT-00006) -- stdin only contains whatever the
		// caller explicitly piped, so it carries none of that risk.
		if contentOrPath != nil && *contentOrPath == "-" {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return fmt.Errorf("reading content from stdin: %w", err)
			}
			s := string(data)
			contentOrPath = &s
		}
		artifact.Content = contentOrPath
	}

	// Report nodes' HTML artifacts must conform to the fixed report
	// template's structural markers (condition 3 of TICK-18D3792B15D92D80):
	// this is what turns "the default report agent should always emit the
	// same fixed HTML format" from an instruction into an enforced
	// contract. Every other node type/artifact type is unaffected.
	if artType == "html" {
		if err := validateReportArtifactIfNeeded(repo, nodeID, contentOrPath); err != nil {
			return err
		}
	}

	created, err := repo.CreateArtifact(artifact)
	if err != nil {
		return err
	}
	return printJSON(created)
}

// validateReportArtifactIfNeeded enforces the fixed report HTML format
// (internal/config.ValidateReportHTML) only when nodeID names a node of type
// "report" -- add-artifact never restricts HTML structure for any other
// node type.
func validateReportArtifactIfNeeded(repo store.GraphRepository, nodeID string, filePath *string) error {
	node, err := repo.GetNode(nodeID)
	if err != nil {
		return err
	}
	if node == nil || node.Type != domain.NodeTypeReport {
		return nil
	}
	if filePath == nil || *filePath == "" {
		return fmt.Errorf("report node %s: html artifact requires a file path", nodeID)
	}
	raw, err := os.ReadFile(*filePath)
	if err != nil {
		return fmt.Errorf("reading report html %s: %w", *filePath, err)
	}
	return config.ValidateReportHTML(string(raw))
}

func cmdGetReviewCriteria(repo store.GraphRepository, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: graph-engine get-review-criteria <nodeId>")
	}
	node, err := repo.GetNode(args[0])
	if err != nil {
		return err
	}
	if node == nil {
		return fmt.Errorf("node %s not found", args[0])
	}
	fmt.Println(engine.GetReviewCriteria(*node))
	return nil
}

func cmdGetWorkflowCatalog(rc runtimeConfig, args []string) error {
	const usage = `usage: graph-engine get-workflow-catalog [--language <code>]`
	var language string
	for i := 0; i < len(args); i++ {
		if args[i] == "--language" && i+1 < len(args) {
			language = args[i+1]
			i++
			continue
		}
		return fmt.Errorf("%s: unrecognized argument %q", usage, args[i])
	}
	catalog, err := config.LoadWithRoots(rc.WorkDir, rc.UserExtensionsDir, rc.TeamExtensionsDir, language)
	if err != nil {
		return err
	}
	// catalog.Language (via Merge) only ever reflects a *persistent*
	// user/team Document.Language -- it never gets language written into it
	// (see config.ResolveLanguage/LoadWithRoots), which is exactly right for
	// get-executable/expand-graph (a real node-creation path should answer
	// "what's persisted", not echo back a one-off --language). But this
	// command exists purely to preview "what would this code resolve to",
	// so when the caller passed --language, the response must say so --
	// otherwise it self-contradicts (node names localized to the override,
	// yet "language" reported as whatever is or isn't persisted). An
	// explicit override always wins in ResolveLanguage regardless of what
	// tiers say, so it wins here identically, without needing to re-derive
	// the tiers ourselves.
	respLanguage := catalog.Language
	if language != "" {
		respLanguage = language
	}
	return printJSON(map[string]any{
		"review_gates": catalog.EnabledReviewGates(),
		"nodes":        catalog.EnabledNodes(),
		"language":     respLanguage,
	})
}

// catalogForTicket is config.LoadWithRoots(rc.WorkDir, ...), except that --
// when ticketID resolves to a ticket whose project has a local path in this
// environment (rc.ProjectPaths, DFLT-00080), and no explicit
// rc.TeamExtensionsDir override is configured -- the team tier is resolved
// from that local path instead of this process's own cwd (rc.WorkDir). This
// is the CLI counterpart of httpserver's Server.loadCatalogForTicket (see
// its doc comment for the full rationale): without it, a project-scoped
// settings edit (always written under that project's local path) would only
// affect `get-executable`/`expand-graph` when this CLI process happens to be
// invoked with the project's directory as its cwd, which does not hold for a
// shared/remote-DB multi-project deployment. An unresolvable ticket
// (including a not-yet-created one), or one belonging to a project with no
// local path in this environment, falls back to the plain rc.WorkDir-based
// resolution unchanged.
//
// language is passed straight through to config.LoadWithRoots as its
// languageOverride (see that function and ResolveLanguage) -- "" reproduces
// the pre-DFLT-00051 behavior of resolving the language solely from
// persistent user/team settings.
func catalogForTicket(repo store.GraphRepository, rc runtimeConfig, ticketID, language string) (config.Catalog, error) {
	if rc.TeamExtensionsDir == "" {
		if ticket, err := repo.GetTicket(ticketID); err == nil && ticket != nil {
			localPath := runtimeconfig.FileConfig{ProjectPaths: rc.ProjectPaths}.ProjectPath(ticket.ProjectID)
			if localPath != "" {
				return config.LoadWithRoots(localPath, rc.UserExtensionsDir, "", language)
			}
		}
	}
	return config.LoadWithRoots(rc.WorkDir, rc.UserExtensionsDir, rc.TeamExtensionsDir, language)
}

// cmdGetLanguageSettings reports whether a persistent language setting
// exists (team tier, else user tier) and, if so, which code it resolves to
// -- so a skill (onboarding, process-ticket) can tell "there's already a
// persistent choice" from "nothing is set yet, decide one for this session"
// without parsing prose out of another command's output (see the execution
// plan's section 1.5). --project <id>, like catalogForTicket, resolves the
// team tier from that project's local path (rc.ProjectPaths) instead of this
// process's own cwd -- a project with no local path keeps rc's own team root,
// never an error -- so the answer matches what get-executable/expand-graph would
// actually use for tickets under that project; omitting it falls back to
// rc's own team root (rc.TeamExtensionsDir / the nearest ancestor
// .graph-ops), matching get-workflow-catalog.
//
// The returned "resolved" value never reflects a call-scoped --language
// override (there is none here -- this command exists precisely to answer
// "what, if anything, is persisted"), only the two persistent tiers.
func cmdGetLanguageSettings(repo store.GraphRepository, rc runtimeConfig, args []string) error {
	const usage = `usage: graph-engine get-language-settings [--project <id>]`
	var projectID string
	for i := 0; i < len(args); i++ {
		if args[i] == "--project" && i+1 < len(args) {
			projectID = args[i+1]
			i++
			continue
		}
		return fmt.Errorf("%s: unrecognized argument %q", usage, args[i])
	}

	roots, err := extensionRoots(rc)
	if err != nil {
		return err
	}
	if projectID != "" && rc.TeamExtensionsDir == "" {
		project, err := repo.GetProject(projectID)
		if err != nil {
			return err
		}
		if project == nil {
			return fmt.Errorf("project not found: %s", projectID)
		}
		if localPath := (runtimeconfig.FileConfig{ProjectPaths: rc.ProjectPaths}).ProjectPath(project.ID); localPath != "" {
			teamRoot, err := config.ProjectTeamRoot(localPath)
			if err != nil {
				return err
			}
			roots.TeamDir = teamRoot
		}
	}

	var userDoc, teamDoc config.Document
	if roots.UserDir != "" {
		userDoc, err = config.LoadDocumentAt(config.UserDocumentPath(roots.UserDir))
		if err != nil {
			return err
		}
	}
	if roots.TeamDir != "" {
		teamDoc, err = config.LoadDocumentAt(config.TeamDocumentPath(roots.TeamDir))
		if err != nil {
			return err
		}
	}

	source := "none"
	switch {
	case teamDoc.Language != "":
		source = "team"
	case userDoc.Language != "":
		source = "user"
	}

	return printJSON(map[string]any{
		"resolved":          config.ResolveLanguage("", userDoc, teamDoc),
		"source":            source,
		"supported_locales": config.SupportedLocales(),
	})
}

// extensionRoots resolves rc's user-/team-extensions directories (env vars /
// graph-config.json overrides, falling back to $HOME/.graph-ops and
// the nearest ancestor .graph-ops directory respectively -- see
// internal/config.ResolveRoots).
func extensionRoots(rc runtimeConfig) (config.Roots, error) {
	return config.ResolveRoots(rc.WorkDir, rc.UserExtensionsDir, rc.TeamExtensionsDir)
}

// cmdGetExtensionRoots surfaces the resolved user-/team-tier extension root
// directories (see internal/config.Roots), so a skill that needs to write
// into that tree directly -- e.g. the onboarding skill persisting a language
// preference into <userDir>/config.yaml and <userDir>/extensions/... -- can
// find the right path without hardcoding $HOME/.graph-ops or
// re-implementing the GRAPH_USER_EXTENSIONS_DIR/graph-config.json precedence
// itself. Either field may be "" (no team root found/configured, or no
// resolvable $HOME).
func cmdGetExtensionRoots(rc runtimeConfig) error {
	roots, err := extensionRoots(rc)
	if err != nil {
		return err
	}
	return printJSON(map[string]string{"userDir": roots.UserDir, "teamDir": roots.TeamDir})
}

// cmdGetSkillContext surfaces the merged user/team extension text for one of
// the plugin's skills (create-ticket/refine-ticket/process-ticket), so
// SKILL.md can fold it into the instructions it gives Claude/subagents
// without packages/plugin ever needing to know where extension files live.
func cmdGetSkillContext(rc runtimeConfig, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: graph-engine get-skill-context <skill-name>")
	}
	roots, err := extensionRoots(rc)
	if err != nil {
		return err
	}
	content := config.ResolveSkillContext(roots, args[0])
	return printJSON(map[string]string{"skill": args[0], "content": content})
}

// cmdGetNodeTypeContext surfaces the merged plugin-default + user + team
// agent instructions for a node type (built-in or a user/team-defined custom
// type). This is what a process-ticket subagent should fetch before doing a
// node's work.
func cmdGetNodeTypeContext(rc runtimeConfig, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: graph-engine get-node-type-context <node-type>")
	}
	roots, err := extensionRoots(rc)
	if err != nil {
		return err
	}
	content := config.ResolveNodeTypeContext(roots, args[0])
	return printJSON(map[string]string{"type": args[0], "content": content})
}

// cmdGetReportTemplate prints the resolved fixed HTML report template
// (team override -> user override -> plugin default) directly to stdout, so
// it can be redirected straight to a file for a report subagent to fill in.
func cmdGetReportTemplate(rc runtimeConfig) error {
	roots, err := extensionRoots(rc)
	if err != nil {
		return err
	}
	fmt.Println(config.ResolveReportTemplate(roots))
	return nil
}

// cmdGetPlanTemplate prints the resolved fixed Markdown plan template (team
// override -> user override -> plugin default) directly to stdout, mirroring
// cmdGetReportTemplate. The language setting does not select a template.
func cmdGetPlanTemplate(rc runtimeConfig) error {
	roots, err := extensionRoots(rc)
	if err != nil {
		return err
	}
	fmt.Println(config.ResolvePlanTemplate(roots))
	return nil
}

// cmdGetReviewTemplate prints the resolved fixed Markdown review template
// (team override -> user override -> plugin default), shared by the
// "review" and "review_gate" node types, directly to stdout, mirroring
// cmdGetReportTemplate. The language setting does not select a template.
func cmdGetReviewTemplate(rc runtimeConfig) error {
	roots, err := extensionRoots(rc)
	if err != nil {
		return err
	}
	fmt.Println(config.ResolveReviewTemplate(roots))
	return nil
}

// cmdServe starts the HTTP API + web UI.
//
// It binds rc.Host (loopback by default -- see runtimeconfig.DefaultHost for
// why, and for how to widen it deliberately), overridable for this one run
// with `--host`.
func cmdServe(rc runtimeConfig, args []string) error {
	port := rc.Port
	host := rc.Host
	for i := 0; i < len(args); i++ {
		if args[i] == "--port" && i+1 < len(args) {
			if p, err := strconv.Atoi(args[i+1]); err == nil {
				port = p
			}
		}
		if args[i] == "--host" && i+1 < len(args) {
			host = args[i+1]
		}
	}

	repo, err := openStore(rc)
	if err != nil {
		return err
	}
	eng := engine.New(repo)

	srv := httpserver.New(repo, eng, httpserver.Config{
		Host:               host,
		DBBackend:          rc.DBBackend,
		DBPath:             rc.DBPath,
		ArtifactsDir:       rc.ArtifactsDir,
		ClaudeBinary:       rc.ClaudeBinary,
		WorkDir:            rc.WorkDir,
		TerminalCommand:    rc.TerminalCommand,
		TerminalWorkDir:    rc.TerminalWorkDir,
		UserExtensionsDir:  rc.UserExtensionsDir,
		TeamExtensionsDir:  rc.TeamExtensionsDir,
		PaginationPageSize: rc.PaginationPageSize,
		HomeDir:            rc.HomeDir,
		// Text (logfmt) so a "request rejected" line greps and reads the
		// same as every other example in README.md. Explicit here (rather
		// than relying on httpserver.New's own os.Stderr fallback) so this
		// is the one obvious place to change it if stdout/a file is ever
		// wanted instead. See internal/httpserver/securitylog.go.
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	})

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	// The advertised URL is derived from the address actually bound, not
	// hardcoded to localhost: with a concrete non-loopback host the two
	// differ, and the hardcoded one is then the URL that does NOT work
	// (DFLT-00023 item N-3).
	fmt.Printf("GraphOps API Server running at %s (listening on %s)\n", humanBaseURL(host, port), addr)
	// One shared definition of "loopback" instead of comparing against three
	// string literals, which wrongly warned about every loopback address
	// outside that list -- 127.0.0.2, for one (DFLT-00023 items N-2/N-4).
	if !runtimeconfig.IsLoopbackHost(host) {
		fmt.Printf("WARNING: this API has no authentication; %s exposes it beyond this machine.\n", addr)
	}
	return http.ListenAndServe(addr, srv.Routes())
}
