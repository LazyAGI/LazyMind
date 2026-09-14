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
