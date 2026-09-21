package modelconfig

import (
	"context"
	"lazymind/core/common/orm"
	"strings"
	"testing"
	"time"
)

func TestValidationPersistenceRechecksConfigurationAndRevokesFailedEvidence(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}, &orm.UserSelectedModel{}, &orm.EvolutionModelValidation{})
	base := orm.BaseModel{CreateUserID: "owner", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	for _, row := range []any{
		&orm.UserModelProvider{ID: "p", Name: "OpenAI", Capabilities: "has_models", BaseModel: base},
		&orm.UserModelProviderGroup{ID: "g", UserModelProviderID: "p", BaseURL: "https://example.test/v1", APIKey: "test-only-secret", IsVerified: true, BaseModel: base},
		&orm.UserModelProviderGroupModel{ID: "m", UserModelProviderID: "p", UserModelProviderGroupID: "g", ProviderName: "OpenAI", Name: "gpt-4o-mini", ModelType: "llm", BaseModel: base},
		&orm.UserSelectedModel{UserID: "owner", ModelKey: "evo_llm", UserModelProviderGroupModelID: "m"},
	} {
		if err := db.DB.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	_, candidate, err := EvolutionValidationConfig(ctx, db.DB, "owner", "m")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := EvolutionValidationConfig(ctx, db.DB, "other", "m"); err == nil {
		t.Fatal("unauthorized validator access")
	}
	report := EvolutionValidationReport{ModelRef: candidate.ModelRef, Nonce: "fixture-nonce", ValidationVersion: EvolutionValidationVersion, Passed: true,
		Checks: map[string]bool{"planning": true, "dataset_generation": true, "evaluation": true, "code_edit": true, "workflow": true}}
	report.Workflow.ThreadID, report.Workflow.AttemptCount = "test-workflow", 20
	report.Workflow.Stages = []string{"dataset", "eval", "analysis", "repair", "abtest"}
	report.Workflow.ArtifactSHA256 = map[string]string{}
	for _, stage := range report.Workflow.Stages {
		report.Workflow.ArtifactSHA256[stage] = strings.Repeat("a", 64)
	}
	if err := SaveEvolutionValidation(ctx, db.DB, "owner", candidate.ModelRef, "fixture-nonce", "test-evidence", report); err != nil {
		t.Fatal(err)
	}
	if _, summary, err := ResolveEvolutionModel(ctx, db.DB, "owner", candidate.ModelRef); err != nil || summary.ValidationStatus != "passed" {
		t.Fatalf("passed report missing: %+v %v", summary, err)
	}
	var evidence orm.EvolutionModelValidation
	if err := db.DB.First(&evidence, "model_ref = ?", candidate.ModelRef).Error; err != nil || !evidence.ExpiresAt.IsZero() {
		t.Fatalf("new workflow report has automatic expiry: %+v %v", evidence, err)
	}
	report.Checks["workflow"] = false
	if err := SaveEvolutionValidation(ctx, db.DB, "owner", candidate.ModelRef, "fixture-nonce", "test-failed-evidence", report); err != nil {
		t.Fatal(err)
	}
	if _, summary, err := ResolveEvolutionModel(ctx, db.DB, "owner", candidate.ModelRef); err != nil || summary.ValidationStatus != "failed" {
		t.Fatalf("workflow failure blocked model or retained passed status: %+v %v", summary, err)
	}
	if err := db.DB.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "g").Update("base_url", "https://changed.example.test/v1").Error; err != nil {
		t.Fatal(err)
	}
	if err := SaveEvolutionValidation(ctx, db.DB, "owner", candidate.ModelRef, "fixture-nonce", "test-stale-evidence", report); err == nil {
		t.Fatal("configuration changed during validation was accepted")
	}
}

func TestEvidenceRequiresEveryRealProbeAndCompleteWorkflow(t *testing.T) {
	report := EvolutionValidationReport{ModelRef: "ref", Nonce: "nonce", ValidationVersion: EvolutionValidationVersion, Passed: true,
		Checks: map[string]bool{"planning": true, "dataset_generation": true, "evaluation": true, "code_edit": true, "workflow": true}}
	if report.Valid("ref", "nonce") {
		t.Fatal("missing workflow evidence accepted")
	}
	report.Workflow.ThreadID = "isolated-thread"
	report.Workflow.AttemptCount = 20
	report.Workflow.Stages = []string{"dataset", "eval", "analysis", "repair", "abtest"}
	report.Workflow.ArtifactSHA256 = map[string]string{}
	for _, stage := range report.Workflow.Stages {
		report.Workflow.ArtifactSHA256[stage] = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	}
	if !report.Valid("ref", "nonce") {
		t.Fatal("complete evidence rejected")
	}
	if report.Valid("other-ref", "nonce") || report.Valid("ref", "old-nonce") {
		t.Fatal("stale report accepted")
	}
	for key := range report.Checks {
		report.Checks[key] = false
		if report.Valid("ref", "nonce") {
			t.Fatalf("missing %s accepted", key)
		}
		report.Checks[key] = true
	}
	report.Failures = map[string]string{"workflow": "validation_failed"}
	if report.Valid("ref", "nonce") {
		t.Fatal("partial failure accepted")
	}
}
