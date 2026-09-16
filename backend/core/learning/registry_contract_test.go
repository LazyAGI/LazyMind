package learning

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCatalogUsesStableSnakeCaseContract(t *testing.T) {
	raw, err := json.Marshal(map[string]any{"capabilities": Capabilities(), "question_types": QuestionTypes(), "profiles": BuiltinProfiles()})
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, key := range []string{`"allowed_question_types"`, `"default_question_types"`, `"label_i18n_key"`, `"capabilities"`} {
		if !strings.Contains(text, key) {
			t.Fatalf("catalog is missing %s: %s", key, text)
		}
	}
	for _, legacy := range []string{`"AllowedQuestionTypes"`, `"Capabilities"`, `"Key"`} {
		if strings.Contains(text, legacy) {
			t.Fatalf("catalog leaked Go field %s: %s", legacy, text)
		}
	}
}

func TestBuiltinProfilesOnlyComposeRegisteredCapabilities(t *testing.T) {
	seen := map[string]bool{}
	for _, profile := range BuiltinProfiles() {
		if seen[profile.Key] {
			t.Fatalf("duplicate profile key %q", profile.Key)
		}
		seen[profile.Key] = true
		if len(profile.Capabilities) == 0 {
			t.Fatalf("profile %q has no capabilities", profile.Key)
		}
		for _, key := range profile.Capabilities {
			if _, ok := CapabilityByKey(key); !ok {
				t.Fatalf("profile %q references unregistered capability %q", profile.Key, key)
			}
		}
	}
	for _, required := range []string{"general", "academic_papers", "chinese_modern", "chinese_classical", "english_learning", "legal", "technical", "historical"} {
		if !seen[required] {
			t.Fatalf("missing built-in profile %q", required)
		}
	}
}
