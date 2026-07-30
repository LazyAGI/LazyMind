package agent

import (
	"net/http"
	"testing"

	"lazymind/core/common/orm"
)

// TestSha256Hex produces a 64-character hex string.
func TestSha256Hex(t *testing.T) {
	got := sha256Hex("hello")
	if len(got) != 64 {
		t.Fatalf("expected 64 hex chars, got %d: %s", len(got), got)
	}
	if sha256Hex("hello") != got {
		t.Fatal("sha256Hex should be deterministic")
	}
}

// TestFirstNonNilAny returns first non-nil value.
func TestFirstNonNilAny(t *testing.T) {
	if got := firstNonNilAny(nil, nil, "found"); got != "found" {
		t.Fatalf("got %v, want found", got)
	}
	if got := firstNonNilAny(nil); got != nil {
		t.Fatalf("all nil: got %v, want nil", got)
	}
}

// TestFirstNonEmptyScalar returns first non-empty string representation.
func TestFirstNonEmptyScalar(t *testing.T) {
	if got := firstNonEmptyScalar(nil, "", "  ", 123); got != "123" {
		t.Fatalf("got %q, want 123", got)
	}
	if got := firstNonEmptyScalar(nil, "", " "); got != "" {
		t.Fatalf("all empty: got %q, want empty", got)
	}
}

// TestFirstPositiveInt returns first positive integer from varied types.
func TestFirstPositiveInt(t *testing.T) {
	tests := []struct {
		name string
		args []any
		want int
	}{
		{"int", []any{int(5), 3}, 5},
		{"int64", []any{int64(10)}, 10},
		{"float64", []any{float64(7.0)}, 7},
		{"string", []any{"15", "20"}, 15},
		{"skip_zero", []any{0, -1, "0", "3"}, 3},
		{"none", []any{0, -1, ""}, 0},
		{"no_args", nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstPositiveInt(tt.args...); got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}

// TestFirstPositiveFloat returns first positive float from varied types.
func TestFirstPositiveFloat(t *testing.T) {
	tests := []struct {
		name string
		args []any
		want float64
	}{
		{"int", []any{int(3), 1}, 3.0},
		{"int64", []any{int64(20)}, 20.0},
		{"float64", []any{float64(2.5)}, 2.5},
		{"string", []any{"3.14", "2.0"}, 3.14},
		{"none", []any{0, -1.0, ""}, 0},
		{"no_args", nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstPositiveFloat(tt.args...); got != tt.want {
				t.Fatalf("got %f, want %f", got, tt.want)
			}
		})
	}
}

// TestParseThreadPageSize defaults/clamps page size values.
func TestParseThreadPageSize(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int
	}{
		{"default_empty", "", 20},
		{"default_whitespace", "   ", 20},
		{"valid", "50", 50},
		{"zero", "0", 20},
		{"negative", "-5", 20},
		{"exceeds_max", "200", 100},
		{"not_a_number", "abc", 20},
		{"max_exact", "100", 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseThreadPageSize(tt.raw); got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}

// TestParseThreadPageToken parses page token strings.
func TestParseThreadPageToken(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		want      int
		wantError bool
	}{
		{"empty", "", 0, false},
		{"whitespace", "  ", 0, false},
		{"valid", "42", 42, false},
		{"zero", "0", 0, false},
		{"negative", "-1", 0, true},
		{"not_a_number", "abc", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseThreadPageToken(tt.raw)
			if tt.wantError && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}

// TestCloneJSONMap produces a deep copy independent of the original.
func TestCloneJSONMap(t *testing.T) {
	orig := map[string]any{"a": "1", "b": map[string]any{"c": "2"}}
	cloned := cloneJSONMap(orig)
	orig["a"] = "changed"
	if cloned["a"] != "1" {
		t.Fatal("clone should not be affected by original mutation")
	}
	// Clone nil returns nil.
	if got := cloneJSONMap(nil); got == nil || len(got) != 0 {
		t.Fatalf("clone nil: got %v, want empty map", got)
	}
}

// TestStringListFromAny extracts string lists from various shapes.
func TestStringListFromAny(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  []string
	}{
		{"nil", nil, []string{}},
		{"string", "hello", []string{"hello"}},
		{"string_slice", []any{"a", "b"}, []string{"a", "b"}},
		{"mixed_slice", []any{"x", 1, nil, 2.5, "y"}, []string{"x", "1", "2.5", "y"}},
		{"empty_slice", []any{}, []string{}},
		{"empty_string", "", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stringListFromAny(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got[%d]=%q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestCsvDataListFromAny extracts rows from slice of map.
func TestCsvDataListFromAny(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  int // row count
	}{
		{"nil", nil, 0},
		{"not_slice", "invalid", 0},
		{"valid_rows", []any{
			map[string]any{"name": "Alice", "age": 30},
			map[string]any{"name": "Bob", "age": 25},
		}, 2},
		{"skip_empty_row", []any{
			map[string]any{},
			map[string]any{"key": "val"},
		}, 1},
		{"trim_key_value", []any{
			map[string]any{"  name ": "  Alice  ", "  ": "empty_key"},
		}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := csvDataListFromAny(tt.input)
			if len(got) != tt.want {
				t.Fatalf("got %d rows, want %d: %v", len(got), tt.want, got)
			}
		})
	}
}

// TestExtractStringByKeys walks nested JSON for matching keys.
func TestExtractStringByKeys(t *testing.T) {
	data := map[string]any{
		"outer": map[string]any{
			"inner": "target",
		},
		"nested": []any{
			map[string]any{"deep": "found"},
		},
	}
	if got := extractStringByKeys(data, "inner", "deep"); got == "" {
		t.Fatal("expected non-empty result")
	}
}

// TestExtractStringByExactKeys matches top-level exact keys only.
func TestExtractStringByExactKeys(t *testing.T) {
	data := map[string]any{
		"title": "Hello",
		"meta":  map[string]any{"title": "nested"},
	}
	if got := extractStringByExactKeys(data, "title"); got != "Hello" {
		t.Fatalf("got %q, want Hello", got)
	}
}

// TestParseJSONValue parses JSON strings into Go values.
func TestParseJSONValue(t *testing.T) {
	if got := parseJSONValue(`{"a":1}`); got == nil {
		t.Fatal("expected non-nil for valid JSON")
	}
	if got := parseJSONValue(""); got != nil {
		t.Fatalf("empty: got %v, want nil", got)
	}
	if got := parseJSONValue("not json"); got != nil {
		t.Fatalf("invalid: got %v, want nil", got)
	}
}

// TestThreadPayloadValue returns parsed JSON or raw string.
func TestThreadPayloadValue(t *testing.T) {
	thread := orm.AgentThread{ThreadPayload: `{"x": 1}`}
	got := threadPayloadValue(thread)
	if got == nil {
		t.Fatal("expected parsed JSON for valid payload")
	}
	thread2 := orm.AgentThread{ThreadPayload: "plain text"}
	if got2 := threadPayloadValue(thread2); got2 != "plain text" {
		t.Fatalf("got %v, want plain text", got2)
	}
}

// TestNewStreamRecordID produces a non-empty, consistent-length ID.
func TestNewStreamRecordID(t *testing.T) {
	id := newStreamRecordID()
	if len(id) == 0 {
		t.Fatal("expected non-empty ID")
	}
	if len(id) != 26 {
		t.Fatalf("expected 26 chars, got %d: %s", len(id), id)
	}
	// Should be unique across calls (at most the counter portion differs).
	id2 := newStreamRecordID()
	if id == id2 {
		t.Fatal("expected different IDs across calls")
	}
}

// TestForwardedUpstreamHeaders copies allowed headers from request.
func TestForwardedUpstreamHeaders(t *testing.T) {
	req, _ := http.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-User-Id", "user-1")
	req.Header.Set("X-Request-Id", "req-abc")

	headers := forwardedUpstreamHeaders(req)
	if headers["Authorization"] != "Bearer token" {
		t.Fatalf("Authorization: got %q", headers["Authorization"])
	}
	if headers["X-User-Id"] != "user-1" {
		t.Fatalf("X-User-Id: got %q", headers["X-User-Id"])
	}
	if headers["X-Request-Id"] != "req-abc" {
		t.Fatalf("X-Request-Id: got %q", headers["X-Request-Id"])
	}
	if headers["Accept"] != "application/json" {
		t.Fatalf("Accept: got %q, want application/json", headers["Accept"])
	}
}

// TestEnsureSSEHeaders sets SSE headers and returns flusher.
func TestEnsureSSEHeaders(t *testing.T) {
	w := &mockResponseWriter{header: http.Header{}}
	flusher, ok := ensureSSEHeaders(w)
	if !ok {
		t.Fatal("expected ok from flusher check")
	}
	if flusher == nil {
		t.Fatal("expected non-nil flusher")
	}
	if w.header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("Content-Type: got %q", w.Header().Get("Content-Type"))
	}
}

// mockResponseWriter implements both http.ResponseWriter and http.Flusher.
type mockResponseWriter struct {
	header     http.Header
	statusCode int
	body       []byte
}

func (w *mockResponseWriter) Header() http.Header { return w.header }
func (w *mockResponseWriter) Write(b []byte) (int, error) {
	w.body = append(w.body, b...)
	return len(b), nil
}
func (w *mockResponseWriter) WriteHeader(code int) { w.statusCode = code }
func (w *mockResponseWriter) Flush()               {}
