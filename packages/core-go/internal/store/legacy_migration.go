package store

import (
	"fmt"
	"log/slog"
)

// testHookBeforeLegacyWorkDirDrop, when non-nil, runs after the DFLT-00080
// migration has seen projects.work_dir but before it issues the DROP COLUMN.
// Tests use it to have another connection drop the column in that window,
// reproducing two processes (parallel CLI calls, or teammates sharing one
// MySQL) running Init at the same time right after an upgrade. Always nil
// outside tests.
var testHookBeforeLegacyWorkDirDrop func()

// dropLegacyProjectsWorkDirColumn is the backend-independent body of the
// DFLT-00080 migration that removes projects.work_dir (its values are not
// carried anywhere -- see SQLiteRepository.dropLegacyProjectsWorkDir).
//
// The check-then-drop is not atomic, so a concurrent Init in another process
// can drop the column between the two steps; this Init's DROP then fails
// ("no such column" on SQLite, error 1091 on MySQL). When the DROP fails the
// column is checked again: if it is already gone, the migration's goal is
// met and the failure is treated as success. Only when the column is still
// there (or the re-check itself fails) is the original DROP error returned.
func dropLegacyProjectsWorkDirColumn(backend string, columnExists func() (bool, error), dropColumn func() error) error {
	has, err := columnExists()
	if err != nil {
		return fmt.Errorf("inspecting projects columns: %w", err)
	}
	if !has {
		return nil
	}
	if testHookBeforeLegacyWorkDirDrop != nil {
		testHookBeforeLegacyWorkDirDrop()
	}
	if dropErr := dropColumn(); dropErr != nil {
		stillHas, checkErr := columnExists()
		if checkErr != nil || stillHas {
			return fmt.Errorf("dropping legacy projects.work_dir column: %w", dropErr)
		}
		// Someone else dropped it first.
		return nil
	}
	// Logged once per DB (the column is gone afterwards), so an operator can
	// tell when the per-project work_dir values -- which are not migrated --
	// disappeared.
	slog.Info("dropped legacy projects.work_dir column; its values were not migrated (set each project's local path again per environment)",
		slog.String("event", "legacy_projects_work_dir_dropped"),
		slog.String("backend", backend))
	return nil
}
