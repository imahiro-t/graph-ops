package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// writeWorkflowYAML writes a minimal team-tier workflow.yaml under
// <workDir>/.graph-ops/workflow.yaml, matching what the settings
// UI's PUT /api/settings/catalog (scope=project) would save.
func writeWorkflowYAML(t *testing.T, workDir, content string) {
	t.Helper()
	dir := filepath.Join(workDir, ".graph-ops")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write workflow.yaml: %v", err)
	}
}

// Scenario: プロジェクト単位設定でのワークフロー編集が、そのプロジェクトの
// チケット実行に反映される -- catalogForTicket resolves the team tier from
// the ticket's own Project.WorkDir, not the CLI process's cwd, so a project
// whose settings were edited via the Web UI (always written under that
// project's work_dir, see internal/httpserver/settings.go) is picked up by
// `get-executable`/`expand-graph` even when this process's cwd is somewhere
// else entirely.
func TestCatalogForTicket_UsesProjectWorkDirNotProcessCWD(t *testing.T) {
	repo := newTestRepo(t)
	projectWorkDir := t.TempDir()
	proj, err := repo.CreateProject("P", "PROJ", projectWorkDir)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	ticket, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	writeWorkflowYAML(t, projectWorkDir, `version: 1
review_gates:
  code_review:
    max_iterations: 7
`)

	// rc.WorkDir deliberately points elsewhere (an unrelated temp dir, never
	// touched by writeWorkflowYAML above) to prove the project's own work_dir
	// -- not the process cwd -- is what gets resolved.
	rc := runtimeConfig{WorkDir: t.TempDir(), UserExtensionsDir: t.TempDir()}
	catalog, err := catalogForTicket(repo, rc, ticket.ID, "")
	if err != nil {
		t.Fatalf("catalogForTicket: %v", err)
	}
	gate, ok := catalog.ReviewGates["code_review"]
	if !ok || gate.MaxIterations == nil || *gate.MaxIterations != 7 {
		t.Fatalf("expected code_review.max_iterations=7 from the project's work_dir, got %+v (ok=%v)", gate, ok)
	}
}

// Scenario: GRAPH_TEAM_EXTENSIONS_DIR等の明示的な環境変数指定がある場合は
// そちらが優先される.
func TestCatalogForTicket_ExplicitTeamExtensionsDirWins(t *testing.T) {
	repo := newTestRepo(t)
	projectWorkDir := t.TempDir()
	proj, err := repo.CreateProject("P", "PROJ", projectWorkDir)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	ticket, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	writeWorkflowYAML(t, projectWorkDir, `version: 1
review_gates:
  code_review:
    max_iterations: 7
`)

	explicitTeamDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(explicitTeamDir, "workflow.yaml"), []byte(`version: 1
review_gates:
  code_review:
    max_iterations: 9
`), 0o644); err != nil {
		t.Fatalf("write explicit workflow.yaml: %v", err)
	}

	rc := runtimeConfig{WorkDir: t.TempDir(), UserExtensionsDir: t.TempDir(), TeamExtensionsDir: explicitTeamDir}
	catalog, err := catalogForTicket(repo, rc, ticket.ID, "")
	if err != nil {
		t.Fatalf("catalogForTicket: %v", err)
	}
	gate, ok := catalog.ReviewGates["code_review"]
	if !ok || gate.MaxIterations == nil || *gate.MaxIterations != 9 {
		t.Fatalf("expected the explicit GRAPH_TEAM_EXTENSIONS_DIR to win (max_iterations=9), got %+v (ok=%v)", gate, ok)
	}
}

// Scenario: `get-workflow-catalog --language ja` は、team/user 階層に
// language が一切永続化されていない環境でも、レスポンスの "language" に
// "ja"（渡した --language そのもの）を反映しなければならない -- ノード名は
// 実際にロケール適用されて日本語化されるのに、"language" フィールドだけ
// "" のままという自己矛盾（code_review verdict art-0844cf66 指摘1）の
// 再発を防ぐための回帰テスト。
func TestCmdGetWorkflowCatalog_LanguageFlagReflectedInResponseLanguageField(t *testing.T) {
	rc := runtimeConfig{WorkDir: t.TempDir(), UserExtensionsDir: t.TempDir(), TeamExtensionsDir: t.TempDir()}

	out := captureStdout(t, func() {
		if err := cmdGetWorkflowCatalog(rc, []string{"--language", "ja"}); err != nil {
			t.Fatalf("cmdGetWorkflowCatalog: %v", err)
		}
	})

	var resp struct {
		Language string `json:"language"`
		Nodes    []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal response: %v\noutput: %s", err, out)
	}

	if resp.Language != "ja" {
		t.Fatalf(`expected "language":"ja" (the explicit --language override) in the response, got %q -- output: %s`, resp.Language, out)
	}

	var implName string
	for _, n := range resp.Nodes {
		if n.ID == "impl" {
			implName = n.Name
		}
	}
	if implName != "実装" {
		t.Fatalf(`expected the "impl" node's name to be localized to "実装" under --language ja, got %q`, implName)
	}
}

// Scenario: --language を渡さない場合は、従来どおり永続設定
// （ここでは何も設定されていないので空文字列）が "language" にそのまま
// 反映される -- 上のテストの修正が、override が無い通常経路を壊していない
// ことの確認。
func TestCmdGetWorkflowCatalog_NoLanguageFlagKeepsPersistedLanguage(t *testing.T) {
	rc := runtimeConfig{WorkDir: t.TempDir(), UserExtensionsDir: t.TempDir(), TeamExtensionsDir: t.TempDir()}

	out := captureStdout(t, func() {
		if err := cmdGetWorkflowCatalog(rc, nil); err != nil {
			t.Fatalf("cmdGetWorkflowCatalog: %v", err)
		}
	})

	var resp struct {
		Language string `json:"language"`
		Nodes    []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal response: %v\noutput: %s", err, out)
	}

	if resp.Language != "" {
		t.Fatalf(`expected "language":"" with nothing persisted and no --language flag, got %q -- output: %s`, resp.Language, out)
	}

	var implName string
	for _, n := range resp.Nodes {
		if n.ID == "impl" {
			implName = n.Name
		}
	}
	if implName != "Implementation" {
		t.Fatalf(`expected the plugin default (English) name "Implementation" for the impl node, got %q`, implName)
	}
}
