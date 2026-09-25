package main

import (
	"fmt"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
	"github.com/graph-ops/core-go/internal/store"
)

const (
	autopilotUsageLine         = "usage: graph-engine autopilot <settings> ..."
	autopilotSettingsUsageLine = "usage: graph-engine autopilot settings [--project <id>]"
)

// cmdAutopilot dispatches `graph-engine autopilot <sub>` (DFLT-00142).
func cmdAutopilot(repo store.GraphRepository, rc runtimeConfig, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", autopilotUsageLine)
	}
	switch args[0] {
	case "settings":
		return cmdAutopilotSettings(repo, rc, args[1:])
	default:
		return fmt.Errorf("unknown autopilot subcommand %q; %s", args[0], autopilotUsageLine)
	}
}

// cmdAutopilotSettings prints the project's effective autopilot settings as
// JSON -- the same shape as GET /api/projects/{id}/autopilot-settings: the
// values in effect, each key's source, locked state and local/team/default
// values, and warnings. It is read-only on purpose: the HTTP API is the one
// write path, so there is exactly one place that validates a save. The
// project is resolved like list-labels' (--project, else cwd's local path,
// else the current project).
func cmdAutopilotSettings(repo store.GraphRepository, rc runtimeConfig, args []string) error {
	var projectFlag string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project":
			if i+1 >= len(args) || args[i+1] == "" {
				return fmt.Errorf("%s: --project needs a value", autopilotSettingsUsageLine)
			}
			if projectFlag != "" {
				return fmt.Errorf("%s: --project given more than once", autopilotSettingsUsageLine)
			}
			projectFlag = args[i+1]
			i++
		default:
			return fmt.Errorf("%s: unexpected argument %q", autopilotSettingsUsageLine, args[i])
		}
	}
	projectID, notice, err := resolveCLIProject(repo, rc, projectFlag)
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
