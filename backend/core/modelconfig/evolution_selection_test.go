package modelconfig

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestEvolutionSelectionDoesNotRequireEvidenceAndRevalidates(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}, &orm.UserSelectedModel{}, &orm.EvolutionModelValidation{})
	now := time.Now().UTC()
	g := orm.UserModelProviderGroup{ID: "g", UserModelProviderID: "p", Name: "test", BaseURL: "https://example.test/v1", APIKey: "test-secret", IsVerified: true, BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}
	m := orm.UserModelProviderGroupModel{ID: "m", UserModelProviderID: "p", UserModelProviderGroupID: "g", ProviderName: "OpenAI", Name: "gpt-4o-mini", ModelType: "llm", BaseModel: g.BaseModel}
	for _, row := range []any{&orm.UserModelProvider{ID: "p", Name: "OpenAI", Capabilities: "has_models", BaseModel: g.BaseModel}, &g, &m, &orm.UserSelectedModel{UserID: "owner", ModelKey: "evo_llm", UserModelProviderGroupModelID: "m", Share: true, CreatedAt: now, UpdatedAt: now}} {
		if err := db.DB.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	list, err := LoadEvolutionModels(ctx, db.DB, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Models) != 1 || !list.CanSelect || list.ConfiguredDefault == nil || list.AvailableDefaultRef != list.ConfiguredDefault.ModelRef {
		t.Fatalf("configured model without workflow evidence unavailable: %+v", list)
	}
	ref := list.ConfiguredDefault.ModelRef
	if list.Models[0].ValidationStatus != "unverified" || list.Models[0].ValidationVersion != "" {
		t.Fatal("missing workflow report presented as validated")
	}
	if _, _, err := ResolveEvolutionModel(ctx, db.DB, "owner", ref); err != nil {
		t.Fatalf("configured model without workflow evidence rejected: %v", err)
	}
	evidence := orm.EvolutionModelValidation{ModelRef: ref, ValidationVersion: EvolutionValidationVersion, EvidenceID: "isolated-e2e-report", Passed: true, VerifiedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}
	if err := db.DB.Create(&evidence).Error; err != nil {
		t.Fatal(err)
	}
	list, err = LoadEvolutionModels(ctx, db.DB, "owner")
	if err != nil || len(list.Models) != 1 || list.AvailableDefaultRef != ref {
		t.Fatalf("verified default missing: %+v, %v", list, err)
	}
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), "test-secret") || strings.Contains(string(raw), "example.test") {
		t.Fatalf("private config leaked: %s", raw)
	}
	// A usable candidate must not silently replace a missing configured default.
	if err := db.DB.Model(&orm.UserSelectedModel{}).Where("user_id = ?", "owner").Update("user_model_provider_group_model_id", "removed-model").Error; err != nil {
		t.Fatal(err)
	}
	list, err = LoadEvolutionModels(ctx, db.DB, "owner")
	if err != nil || len(list.Models) != 1 || list.AvailableDefaultRef != "" {
		t.Fatalf("missing default silently replaced: %+v %v", list, err)
	}
	if _, _, err := ResolveEvolutionModel(ctx, db.DB, "owner", ""); err == nil {
		t.Fatal("missing default resolved implicitly")
	}
	if err := db.DB.Model(&orm.UserSelectedModel{}).Where("user_id = ?", "owner").Update("user_model_provider_group_model_id", "m").Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveEvolutionModel(ctx, db.DB, "owner", "cloud:m"); err == nil {
		t.Fatal("foreign model reference accepted as a local ID")
	}
	for _, report := range []struct {
		update map[string]any
		status string
	}{
		{map[string]any{"passed": false}, "failed"},
		{map[string]any{"expires_at": now.Add(-time.Second)}, "passed"},
		{map[string]any{"verified_at": now.Add(time.Hour)}, "unverified"},
		{map[string]any{"validation_version": "old-version"}, "unverified"},
		{map[string]any{"evidence_id": ""}, "unverified"},
	} {
		if err := db.DB.Model(&evidence).Updates(report.update).Error; err != nil {
			t.Fatal(err)
		}
		_, summary, err := ResolveEvolutionModel(ctx, db.DB, "owner", ref)
		if err != nil || summary.ValidationStatus != report.status {
			t.Fatalf("workflow report changed admission or was misrepresented: %+v %v", summary, err)
		}
		if err := db.DB.Model(&evidence).Updates(map[string]any{"passed": true, "expires_at": now.Add(time.Hour), "verified_at": now.Add(-time.Minute), "validation_version": EvolutionValidationVersion, "evidence_id": "isolated-e2e-report"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	config, summary, err := ResolveEvolutionModel(ctx, db.DB, "other", ref)
	if err != nil || config["model"] != m.Name || summary.Source != "shared" {
		t.Fatalf("shared selection failed: %+v %v", summary, err)
	}
	if err := db.DB.Model(&orm.UserSelectedModel{}).Where("user_id = ?", "owner").Update("share", false).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveEvolutionModel(ctx, db.DB, "other", ref); err == nil {
		t.Fatal("revoked sharing still authorized")
	}
	if err := db.DB.Model(&g).Update("credential_version", 2).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveEvolutionModel(ctx, db.DB, "owner", ref); err == nil {
		t.Fatal("stale configuration accepted")
	}
	list, err = LoadEvolutionModels(ctx, db.DB, "owner")
	if err != nil || len(list.Models) != 1 || list.Models[0].ValidationStatus != "unverified" {
		t.Fatalf("changed configuration must remain selectable without reusing old report: %+v %v", list, err)
	}
	for _, invalid := range []struct {
		update map[string]any
		reason string
	}{
		{map[string]any{"is_verified": false}, "connection_unverified"},
		{map[string]any{"base_url": ""}, "configuration_incomplete"},
		{map[string]any{"api_key": ""}, "configuration_incomplete"},
		{map[string]any{"api_key_ciphertext": "invalid-test-ciphertext"}, "credentials_unavailable"},
	} {
		if err := db.DB.Model(&g).Updates(invalid.update).Error; err != nil {
			t.Fatal(err)
		}
		list, err = LoadEvolutionModels(ctx, db.DB, "owner")
		if err != nil || list.CanSelect || len(list.Models) != 0 || list.UnavailableReason != invalid.reason {
			t.Fatalf("invalid connection admitted or wrong reason: %+v %v", list, err)
		}
		if _, _, err := ResolveEvolutionModel(ctx, db.DB, "owner", list.ConfiguredDefault.ModelRef); err == nil {
			t.Fatal("creation accepted unavailable configuration")
		}
		if err := db.DB.Model(&g).Updates(map[string]any{"is_verified": true, "base_url": "https://example.test/v1", "api_key": "test-secret", "api_key_ciphertext": ""}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.DB.Model(&m).Update("model_type", "embed").Error; err != nil {
		t.Fatal(err)
	}
	list, err = LoadEvolutionModels(ctx, db.DB, "owner")
	if err != nil || list.CanSelect || list.UnavailableReason != "incompatible" {
		t.Fatalf("incompatible executor model admitted: %+v %v", list, err)
	}
	if _, _, err := ResolveEvolutionModel(ctx, db.DB, "owner", list.ConfiguredDefault.ModelRef); err == nil {
		t.Fatal("creation accepted incompatible executor model")
	}
}
