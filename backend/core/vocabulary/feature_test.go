package vocabulary

import "testing"

func TestVocabularyIgnoresLegacyDisableFlag(t *testing.T) {
	t.Setenv("LAZYMIND_VOCABULARY_ENABLED", "false")
	if !Enabled() {
		t.Fatal("vocabulary must remain available")
	}
}

func TestAnkiDeploymentCapabilityRequiresOptIn(t *testing.T) {
	for _, value := range []string{"", "false", "true"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("LAZYMIND_VOCABULARY_ANKI_SYNC_ENABLED", value)
			if ankiSyncAvailable() != (value == "true") {
				t.Fatalf("unexpected Anki sync availability for %q", value)
			}
		})
	}
}
