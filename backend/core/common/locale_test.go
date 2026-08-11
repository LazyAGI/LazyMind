package common

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNormalizeLocale maps Accept-Language values to zh-CN / en-US,
// covering quality weights, unknown languages, and empty input.
func TestNormalizeLocale(t *testing.T) {
	tests := []struct {
		name           string
		acceptLanguage string
		want           string
	}{
		{"empty", "", LocaleZhCN},
		{"zh", "zh", LocaleZhCN},
		{"zh-CN", "zh-CN", LocaleZhCN},
		{"zh-TW", "zh-TW", LocaleZhCN},
		{"en", "en", LocaleEnUS},
		{"en-US", "en-US", LocaleEnUS},
		{"en-GB", "en-GB", LocaleEnUS},
		{"with quality zh", "zh-CN,zh;q=0.9", LocaleZhCN},
		{"with quality en first", "en-US,en;q=0.9,zh-CN;q=0.8", LocaleEnUS},
		{"with quality zh first", "zh-CN,zh;q=0.9,en;q=0.8", LocaleZhCN},
		{"multiple zh variant", "zh-TW,zh-HK;q=0.9", LocaleZhCN},
		{"unknown language", "fr-FR", LocaleZhCN},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeLocale(tt.acceptLanguage)
			if got != tt.want {
				t.Fatalf("NormalizeLocale(%q) = %q, want %q", tt.acceptLanguage, got, tt.want)
			}
		})
	}
}

// TestSetLanguageResponseHeaders_SetsContentLanguage checks the Content-Language header value.
func TestSetLanguageResponseHeaders_SetsContentLanguage(t *testing.T) {
	w := httptest.NewRecorder()
	SetLanguageResponseHeaders(w, "zh-CN")
	if got := w.Header().Get("Content-Language"); got != LocaleZhCN {
		t.Fatalf("Content-Language = %q, want %q", got, LocaleZhCN)
	}
}

// TestSetLanguageResponseHeaders_SetsVary checks that Vary includes Accept-Language.
func TestSetLanguageResponseHeaders_SetsVary(t *testing.T) {
	w := httptest.NewRecorder()
	SetLanguageResponseHeaders(w, "zh-CN")
	vary := w.Header().Get("Vary")
	if !strings.Contains(strings.ToLower(vary), "accept-language") {
		t.Fatalf("Vary = %q, want Accept-Language", vary)
	}
}

// TestSetLanguageResponseHeaders_NormalizesLocale checks that the locale is normalized before setting.
func TestSetLanguageResponseHeaders_NormalizesLocale(t *testing.T) {
	w := httptest.NewRecorder()
	SetLanguageResponseHeaders(w, "en-GB")
	if got := w.Header().Get("Content-Language"); got != LocaleEnUS {
		t.Fatalf("Content-Language = %q, want %q", got, LocaleEnUS)
	}
}

// TestSetLanguageResponseHeaders_NoDuplicateVary ensures Vary is not duplicated.
func TestSetLanguageResponseHeaders_NoDuplicateVary(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Set("Vary", "Accept-Language")
	SetLanguageResponseHeaders(w, "zh-CN")
	values := w.Header().Values("Vary")
	count := 0
	for _, v := range values {
		for _, item := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(item), "Accept-Language") {
				count++
			}
		}
	}
	if count != 1 {
		t.Fatalf("Accept-Language appears %d times in Vary, want 1; values=%v", count, values)
	}
}

// TestSetLanguageResponseHeaders_AddsVaryToExisting checks Vary is appended when other values exist.
func TestSetLanguageResponseHeaders_AddsVaryToExisting(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Set("Vary", "Origin")
	SetLanguageResponseHeaders(w, "en-US")
	allVary := strings.Join(w.Header().Values("Vary"), ",")
	if !strings.Contains(strings.ToLower(allVary), "accept-language") {
		t.Fatalf("expected Vary to contain Accept-Language, got %q", allVary)
	}
}
