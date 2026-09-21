package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeWorkflowYAML writes a team-tier workflow.yaml under
// <root>/.graph-ops/workflow.yaml -- the layout a repository that used to be
// picked up as a team tier would have.
func writeWorkflowYAML(t *testing.T, root, content string) {
	t.Helper()
	dir := filepath.Join(root, ".graph-ops")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write workflow.yaml: %v", err)
	}
}

// TestCmdGetWorkflowCatalog_RepoGraphOpsIsNotATeamTier is completion
// criterion 6 at the CLI level: neither the process's own directory nor a
// project's local path may supply a workflow.yaml. Both used to -- the
// former through config.findProjectDir's walk, the latter through
// catalogForTicket's per-project resolution (DFLT-00080) -- so both are
// checked here in one go.
func TestCmdGetWorkflowCatalog_RepoGraphOpsIsNotATeamTier(t *testing.T) {
	cwd := tempCwd(t)
	stubHome(t)
	writeWorkflowYAML(t, cwd, `version: 1
review_gates:
  code_review:
    max_iterations: 5
`)
	projectLocalPath := t.TempDir()
	writeWorkflowYAML(t, projectLocalPath, `version: 1
review_gates:
  code_review:
    max_iterations: 7
`)

	rc := runtimeConfig{
		WorkDir:           cwd,
		UserExtensionsDir: t.TempDir(),
		ProjectPaths:      map[string]string{"proj-x": projectLocalPath},
	}
	gate := codeReviewGateFromCatalog(t, rc, nil)
	// Neither 5 nor 7: the plugin default survives untouched, because
	// neither workflow.yaml was read at all.
	if gate.MaxIterations == nil || *gate.MaxIterations == 5 || *gate.MaxIterations == 7 {
		t.Errorf("code_review.max_iterations = %v; no workflow.yaml outside teamExtensionsDir may be read", gate.MaxIterations)
	}
}

// TestCmdGetWorkflowCatalog_ExplicitTeamExtensionsDirIsRead is the other
// half: the one configured location still works, so team sharing is intact.
func TestCmdGetWorkflowCatalog_ExplicitTeamExtensionsDirIsRead(t *testing.T) {
	cwd := tempCwd(t)
	stubHome(t)
	writeWorkflowYAML(t, cwd, `version: 1
review_gates:
  code_review:
    max_iterations: 5
`)
	shared := t.TempDir()
	if err := os.WriteFile(filepath.Join(shared, "workflow.yaml"), []byte(`version: 1
review_gates:
  code_review:
    max_iterations: 9
`), 0o644); err != nil {
		t.Fatalf("write workflow.yaml: %v", err)
	}

	rc := runtimeConfig{WorkDir: cwd, UserExtensionsDir: t.TempDir(), TeamExtensionsDir: shared}
	gate := codeReviewGateFromCatalog(t, rc, nil)
	if gate.MaxIterations == nil || *gate.MaxIterations != 9 {
		t.Fatalf("expected max_iterations=9 from teamExtensionsDir, got %+v", gate)
	}
}

// reviewGate is one entry of get-workflow-catalog's review_gates map.
type reviewGate struct {
	MaxIterations *int `json:"max_iterations"`
}

// codeReviewGateFromCatalog runs get-workflow-catalog and returns its
// code_review gate.
func codeReviewGateFromCatalog(t *testing.T, rc runtimeConfig, args []string) reviewGate {
	t.Helper()
	out := captureStdout(t, func() {
		if err := cmdGetWorkflowCatalog(rc, args); err != nil {
			t.Fatalf("cmdGetWorkflowCatalog: %v", err)
		}
	})
	var resp struct {
		ReviewGates map[string]reviewGate `json:"review_gates"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, out)
	}
	gate, ok := resp.ReviewGates["code_review"]
	if !ok {
		t.Fatalf("no code_review gate in %s", out)
	}
	return gate
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
