package translation

import "testing"

func TestNormalizeTarget(t *testing.T) {
	if got := normalizeTarget("", "hello world"); got != "zh" {
		t.Fatalf("English target = %q", got)
	}
	if got := normalizeTarget("", "你好 world"); got != "en" {
		t.Fatalf("Chinese target = %q", got)
	}
	if got := normalizeTarget("EN", "hello"); got != "en" {
		t.Fatalf("explicit target = %q", got)
	}
}
