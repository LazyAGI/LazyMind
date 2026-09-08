package translation

import "testing"

func TestParseTencentCredentials(t *testing.T) {
	got, err := parseTencentCredentials(" AKIDexample | secret-value ")
	if err != nil {
		t.Fatalf("parseTencentCredentials: %v", err)
	}
	if got.SecretID != "AKIDexample" || got.SecretKey != "secret-value" {
		t.Fatalf("unexpected credentials: %#v", got)
	}
	if _, err := parseTencentCredentials("only-one-value"); err == nil {
		t.Fatal("expected an invalid credential error")
	}
}

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
