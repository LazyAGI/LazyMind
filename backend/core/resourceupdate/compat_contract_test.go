package resourceupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlan2CodeDoesNotDependOnLegacyAsyncJobSuggestionOrGenerateContracts(t *testing.T) {
	files := []string{
		filepath.Join("..", "common", "orm", "resource_update_models.go"),
		filepath.Join("..", "algo", "review_client.go"),
		filepath.Join("..", "algo", "review_types.go"),
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read resourceupdate package: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		files = append(files, entry.Name())
	}

	forbidden := []string{
		"async_jobs",
		"async_job_id",
		"AvailableTools",
		"suggestion_ids",
		"suggestion_status",
		"has_pending_review_suggestions",
		"draft_suggestion_ids",
		"resource_suggestions",
		"resource_session_snapshots",
		"ResourceSuggestion",
		"DraftSuggestionIDs",
		"WithDraftSuggestionIDs",
		"LoadApprovedSuggestions",
		"LoadAutoApplicableSuggestions",
		"GenerateSkill",
		"GenerateMemory",
		"GenerateUserPreference",
		"/api/chat/skill/generate",
		"/api/chat/memory/generate",
		"/api/chat/user_preference/generate",
	}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		content := string(body)
		for _, term := range forbidden {
			if allowedLegacyCleanupReference(file, term, content) {
				continue
			}
			if strings.Contains(content, term) {
				t.Fatalf("Plan2 code must not contain %q in %s", term, file)
			}
		}
	}
}

func allowedLegacyCleanupReference(file, term, content string) bool {
	return file == "results.go" &&
		(term == "suggestion_ids" || term == "draft_suggestion_ids") &&
		strings.Contains(content, `delete(payload, "draft_suggestion_ids")`)
}
