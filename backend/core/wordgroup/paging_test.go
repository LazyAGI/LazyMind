package wordgroup

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

// makePageToken helper encodes a token payload for test input.
func makePageToken(t *testing.T, key string, value any) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{key: value})
	if err != nil {
		t.Fatalf("marshal token: %v", err)
	}
	return base64.RawStdEncoding.EncodeToString(b)
}

// TestParseListPageToken_EmptyToken returns offset 0.
func TestParseListPageToken_EmptyToken(t *testing.T) {
	offset, err := parseListPageToken("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if offset != 0 {
		t.Fatalf("offset: got %d, want 0", offset)
	}
}

// TestParseListPageToken_PositiveInteger token is parsed as a raw offset number.
func TestParseListPageToken_PositiveInteger(t *testing.T) {
	offset, err := parseListPageToken("42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if offset != 42 {
		t.Fatalf("offset: got %d, want 42", offset)
	}
}

// TestParseListPageToken_ZeroInteger parses "0" as offset 0.
func TestParseListPageToken_ZeroInteger(t *testing.T) {
	offset, err := parseListPageToken("0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if offset != 0 {
		t.Fatalf("offset: got %d, want 0", offset)
	}
}

// TestParseListPageToken_NegativeInteger is rejected.
func TestParseListPageToken_NegativeInteger(t *testing.T) {
	_, err := parseListPageToken("-1")
	if err == nil {
		t.Fatal("expected error for negative integer token")
	}
}

// TestParseListPageToken_StringIntegerInBase64 parses a string float in a base64 JSON payload.
func TestParseListPageToken_StringIntegerInBase64(t *testing.T) {
	token := makePageToken(t, "Start", "99")
	offset, err := parseListPageToken(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if offset != 99 {
		t.Fatalf("offset: got %d, want 99", offset)
	}
}

// TestParseListPageToken_FloatInBase64 parses a JSON float as an integer offset.
func TestParseListPageToken_FloatInBase64(t *testing.T) {
	token := makePageToken(t, "start", float64(30))
	offset, err := parseListPageToken(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if offset != 30 {
		t.Fatalf("offset: got %d, want 30", offset)
	}
}

// TestParseListPageToken_OffsetKey parses the "offset" key in a base64 token.
func TestParseListPageToken_OffsetKey(t *testing.T) {
	token := makePageToken(t, "offset", 15)
	offset, err := parseListPageToken(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if offset != 15 {
		t.Fatalf("offset: got %d, want 15", offset)
	}
}

// TestParseListPageToken_StdEncoding supports standard base64 padding.
func TestParseListPageToken_StdEncoding(t *testing.T) {
	b, _ := json.Marshal(map[string]any{"Start": 7})
	token := base64.StdEncoding.EncodeToString(b)
	offset, err := parseListPageToken(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if offset != 7 {
		t.Fatalf("offset: got %d, want 7", offset)
	}
}

// TestParseListPageToken_URLEncoding supports URL-safe base64.
func TestParseListPageToken_URLEncoding(t *testing.T) {
	b, _ := json.Marshal(map[string]any{"Start": 3})
	token := base64.URLEncoding.EncodeToString(b)
	offset, err := parseListPageToken(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if offset != 3 {
		t.Fatalf("offset: got %d, want 3", offset)
	}
}

// TestParseListPageToken_RawURLEncoding supports raw URL-safe base64.
func TestParseListPageToken_RawURLEncoding(t *testing.T) {
	b, _ := json.Marshal(map[string]any{"Start": 3})
	token := base64.RawURLEncoding.EncodeToString(b)
	offset, err := parseListPageToken(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if offset != 3 {
		t.Fatalf("offset: got %d, want 3", offset)
	}
}

// TestParseListPageToken_InvalidBase64 returns an error.
func TestParseListPageToken_InvalidBase64(t *testing.T) {
	_, err := parseListPageToken("not-valid-base64!!!")
	if err == nil {
		t.Fatal("expected error for invalid base64 token")
	}
}

// TestParseListPageToken_Whitespace trims surrounding whitespace.
func TestParseListPageToken_Whitespace(t *testing.T) {
	offset, err := parseListPageToken("  10  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if offset != 10 {
		t.Fatalf("offset: got %d, want 10", offset)
	}
}

// TestEncodeListPageToken produces a decodable RawStdEncoding token.
func TestEncodeListPageToken(t *testing.T) {
	token := encodeListPageToken(100, 20, 200)
	offset, err := parseListPageToken(token)
	if err != nil {
		t.Fatalf("parse generated token: %v", err)
	}
	if offset != 100 {
		t.Fatalf("round-trip offset: got %d, want 100", offset)
	}
}

// TestEncodeListPageToken_ZeroOffset produces a decodable token.
func TestEncodeListPageToken_ZeroOffset(t *testing.T) {
	token := encodeListPageToken(0, 10, 50)
	offset, err := parseListPageToken(token)
	if err != nil {
		t.Fatalf("parse generated token: %v", err)
	}
	if offset != 0 {
		t.Fatalf("round-trip offset: got %d, want 0", offset)
	}
}

// TestEscapeLikePatternForBangEscape escapes special characters for SQL LIKE with ! as escape.
func TestEscapeLikePatternForBangEscape(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain text", "hello", "hello"},
		{"exclamation", "a!b", "a!!b"},
		{"percent", "50%", "50!%"},
		{"underscore", "a_b", "a!_b"},
		{"mixed specials", "!50%_abc", "!!50!%!_abc"},
		{"empty string", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := escapeLikePatternForBangEscape(tt.input)
			if got != tt.want {
				t.Fatalf("escapeLikePatternForBangEscape(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
