package modelconfig

import "testing"

func TestChatModelFallbackEnabled(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{{"", true}, {"true", true}, {"false", false}, {" FALSE ", false}, {"invalid", true}} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("LAZYMIND_CHAT_MODEL_FALLBACK_ENABLED", tc.value)
			if got := ChatModelFallbackEnabled(); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
