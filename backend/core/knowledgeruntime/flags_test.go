package knowledgeruntime

import "testing"

func TestManagementReleaseAndRollback(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", true}, {"true", true}, {"  TRUE  ", true}, {"1", true},
		{"false", false}, {"0", false}, {"invalid", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("LAZYMIND_KNOWLEDGE_MANAGEMENT_ENABLED", tc.value)
			if got := Enabled(); got != tc.want {
				t.Fatalf("Enabled()=%v, want %v", got, tc.want)
			}
		})
	}
}
