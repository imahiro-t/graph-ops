package httpserver

import (
	"net/http"
	"testing"
)

// Scenario: スキルの補足指示を追加・保存でき、空文字でクリアすると既定
// （補足指示なし）に戻る.
func TestSettingsSkill_SaveAndClear(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/skills/create-ticket", map[string]any{
		"text": "Always ask for the target release train.",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/settings/skills/create-ticket", nil)
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
	rec = doJSON(t, s, http.MethodPut, "/api/settings/skills/create-ticket", map[string]any{"text": ""})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT clear expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/settings/skills/create-ticket", nil)
	mustDecode(t, rec, &body)
	if body.TierText != "" {
		t.Errorf("tier_text after clear = %q, want empty", body.TierText)
	}
	if body.MergedText != "" {
		t.Errorf("merged_text after clear = %q, want empty", body.MergedText)
	}
}

// Scenario: GET /api/settings/skills はスキル一覧を固定順で返し、保存後は
// 該当スキルの has_user_override が true になる。has_team_override は
// プロジェクト単位設定の廃止とともに消えており、レスポンスに存在しない.
func TestSettingsSkills_ListReflectsOverrides(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodGet, "/api/settings/skills", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Skills []struct {
			Name            string `json:"name"`
			HasUserOverride bool   `json:"has_user_override"`
		} `json:"skills"`
	}
	mustDecode(t, rec, &body)
	wantNames := []string{"create-ticket", "refine-ticket", "process-ticket", "onboarding",
		"autopilot-ticket", "autopilot-tree", "autopilot-worker"}
	if len(body.Skills) != len(wantNames) {
		t.Fatalf("skills = %+v, want %d entries", body.Skills, len(wantNames))
	}
	for i, name := range wantNames {
		if body.Skills[i].Name != name {
			t.Errorf("skills[%d].name = %q, want %q", i, body.Skills[i].Name, name)
		}
		if body.Skills[i].HasUserOverride {
			t.Errorf("skills[%d] = %+v, want no override before any save", i, body.Skills[i])
		}
	}

	rec = doJSON(t, s, http.MethodPut, "/api/settings/skills/onboarding", map[string]any{
		"text": "onboarding tweak",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/settings/skills", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	mustDecode(t, rec, &body)
	for _, sk := range body.Skills {
		if sk.Name == "onboarding" && !sk.HasUserOverride {
			t.Errorf("onboarding has_user_override = false, want true")
		}
	}

	// has_team_override is gone from the wire, not merely always false.
	var raw struct {
		Skills []map[string]any `json:"skills"`
	}
	mustDecode(t, rec, &raw)
	for _, sk := range raw.Skills {
		if _, present := sk["has_team_override"]; present {
			t.Errorf("skill entry still carries has_team_override: %v", sk)
		}
	}
}
