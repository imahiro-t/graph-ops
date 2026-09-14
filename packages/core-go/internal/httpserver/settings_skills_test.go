package httpserver

import (
	"net/http"
	"testing"
)

// Scenario: 全体設定スコープでスキルの補足指示を追加・保存でき、空文字で
// クリアすると既定（補足指示なし）に戻る.
func TestSettingsSkill_GlobalScopeSaveAndClear(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/skills/create-ticket", map[string]any{
		"scope": "global", "text": "Always ask for the target release train.",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/settings/skills/create-ticket?scope=global", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Name       string `json:"name"`
		TierText   string `json:"tier_text"`
		MergedText string `json:"merged_text"`
	}
	mustDecode(t, rec, &body)
	if body.Name != "create-ticket" {
		t.Errorf("name = %q", body.Name)
	}
	if body.TierText != "Always ask for the target release train." {
		t.Errorf("tier_text = %q", body.TierText)
	}
	if !contains(body.MergedText, "Always ask for the target release train.") {
		t.Errorf("merged_text missing override: %q", body.MergedText)
	}

	// Clearing (empty text) removes the override entirely -- there is no
	// plugin-default layer for skill context, so merged_text goes back to "".
	rec = doJSON(t, s, http.MethodPut, "/api/settings/skills/create-ticket", map[string]any{
		"scope": "global", "text": "",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT clear expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/settings/skills/create-ticket?scope=global", nil)
	mustDecode(t, rec, &body)
	if body.TierText != "" {
		t.Errorf("tier_text after clear = %q, want empty", body.TierText)
	}
	if body.MergedText != "" {
		t.Errorf("merged_text after clear = %q, want empty", body.MergedText)
	}
}

// Scenario: プロジェクト単位設定スコープでスキルの補足指示を保存でき、
// global スコープ側には影響しない（tier分離の確認）.
func TestSettingsSkill_ProjectScopeIsolatedFromGlobal(t *testing.T) {
	s, _, projA := newSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/skills/refine-ticket", map[string]any{
		"scope": "project", "project_id": projA, "text": "project-A refine instructions",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/settings/skills/refine-ticket?scope=project&project_id="+projA, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		TierText   string `json:"tier_text"`
		MergedText string `json:"merged_text"`
	}
	mustDecode(t, rec, &body)
	if body.TierText != "project-A refine instructions" {
		t.Errorf("tier_text = %q", body.TierText)
	}

	// Global scope sees no override.
	rec = doJSON(t, s, http.MethodGet, "/api/settings/skills/refine-ticket?scope=global", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	mustDecode(t, rec, &body)
	if body.TierText != "" {
		t.Errorf("global tier_text = %q, want empty (isolated from project A)", body.TierText)
	}
}

// Scenario: GET /api/settings/skills はスキル一覧を固定順で返し、保存後は
// 該当スキルの has_user_override / has_team_override が true になる.
func TestSettingsSkills_ListReflectsOverrides(t *testing.T) {
	s, _, projA := newSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodGet, "/api/settings/skills", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Skills []struct {
			Name            string `json:"name"`
			HasUserOverride bool   `json:"has_user_override"`
			HasTeamOverride bool   `json:"has_team_override"`
		} `json:"skills"`
	}
	mustDecode(t, rec, &body)
	wantNames := []string{"create-ticket", "refine-ticket", "process-ticket", "onboarding"}
	if len(body.Skills) != len(wantNames) {
		t.Fatalf("skills = %+v, want %d entries", body.Skills, len(wantNames))
	}
	for i, name := range wantNames {
		if body.Skills[i].Name != name {
			t.Errorf("skills[%d].name = %q, want %q", i, body.Skills[i].Name, name)
		}
		if body.Skills[i].HasUserOverride || body.Skills[i].HasTeamOverride {
			t.Errorf("skills[%d] = %+v, want no overrides before any save", i, body.Skills[i])
		}
	}

	rec = doJSON(t, s, http.MethodPut, "/api/settings/skills/onboarding", map[string]any{
		"scope": "global", "text": "global onboarding tweak",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodPut, "/api/settings/skills/process-ticket", map[string]any{
		"scope": "project", "project_id": projA, "text": "project-A process tweak",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/settings/skills?project_id="+projA, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	mustDecode(t, rec, &body)
	for _, sk := range body.Skills {
		switch sk.Name {
		case "onboarding":
			if !sk.HasUserOverride {
				t.Errorf("onboarding has_user_override = false, want true")
			}
		case "process-ticket":
			if !sk.HasTeamOverride {
				t.Errorf("process-ticket has_team_override = false, want true")
			}
		}
	}
}

// Scenario: 存在しない/不正な scope パラメータで400になる
// （resolveSettingsScope 経由、既存パターンの踏襲確認）.
func TestSettingsSkill_InvalidScopeRejected(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)
	rec := doJSON(t, s, http.MethodGet, "/api/settings/skills/create-ticket?scope=bogus", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeError(t, rec).Code; got != "INVALID_SETTINGS_SCOPE" {
		t.Errorf("error code = %q", got)
	}
}
