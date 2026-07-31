package agent

import (
	"encoding/json"
	"testing"
)

// TestAgentScalarString_Nil returns empty string for nil.
func TestAgentScalarString_Nil(t *testing.T) {
	if got := agentScalarString(nil); got != "" {
		t.Fatalf("nil: got %q, want empty", got)
	}
}

// TestAgentScalarString_String returns the string as-is.
func TestAgentScalarString_String(t *testing.T) {
	if got := agentScalarString("hello"); got != "hello" {
		t.Fatalf("got %q, want hello", got)
	}
}

// TestAgentScalarString_JsonNumber returns its string representation.
func TestAgentScalarString_JsonNumber(t *testing.T) {
	if got := agentScalarString(json.Number("42")); got != "42" {
		t.Fatalf("got %q, want 42", got)
	}
}

// TestAgentScalarString_Bool returns "true" or "false".
func TestAgentScalarString_Bool(t *testing.T) {
	tests := []struct {
		input bool
		want  string
	}{
		{true, "true"},
		{false, "false"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := agentScalarString(tt.input); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestAgentScalarString_Float converts float to string without scientific notation.
func TestAgentScalarString_Float(t *testing.T) {
	if got := agentScalarString(3.14); got != "3.14" {
		t.Fatalf("got %q, want 3.14", got)
	}
	if got := agentScalarString(float32(1.5)); got != "1.5" {
		t.Fatalf("float32: got %q, want 1.5", got)
	}
}

// TestAgentScalarString_Int converts various int types to string.
func TestAgentScalarString_Int(t *testing.T) {
	if got := agentScalarString(42); got != "42" {
		t.Fatalf("int: got %q, want 42", got)
	}
	if got := agentScalarString(int64(100)); got != "100" {
		t.Fatalf("int64: got %q, want 100", got)
	}
	if got := agentScalarString(uint(7)); got != "7" {
		t.Fatalf("uint: got %q, want 7", got)
	}
}

// TestAgentScalarString_Bytes converts []byte to string.
func TestAgentScalarString_Bytes(t *testing.T) {
	if got := agentScalarString([]byte("raw bytes")); got != "raw bytes" {
		t.Fatalf("got %q, want raw bytes", got)
	}
}

// TestAgentScalarString_Map marshals map to JSON string.
func TestAgentScalarString_Map(t *testing.T) {
	got := agentScalarString(map[string]any{"key": "val"})
	expected := `{"key":"val"}`
	if got != expected {
		t.Fatalf("got %q, want %q", got, expected)
	}
}

// TestAgentScalarString_Slice marshals slice to JSON string.
func TestAgentScalarString_Slice(t *testing.T) {
	got := agentScalarString([]int{1, 2, 3})
	expected := `[1,2,3]`
	if got != expected {
		t.Fatalf("got %q, want %q", got, expected)
	}
}

// TestAgentScalarString_Struct marshals struct to JSON string.
func TestAgentScalarString_Struct(t *testing.T) {
	type S struct{ Name string }
	got := agentScalarString(S{Name: "test"})
	expected := `{"Name":"test"}`
	if got != expected {
		t.Fatalf("got %q, want %q", got, expected)
	}
}

// TestFirstNonEmptyString_NoValues returns empty string.
func TestFirstNonEmptyString_NoValues(t *testing.T) {
	if got := firstNonEmptyString(); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// TestFirstNonEmptyString_SingleValue returns trimmed value.
func TestFirstNonEmptyString_SingleValue(t *testing.T) {
	if got := firstNonEmptyString(" hello "); got != "hello" {
		t.Fatalf("got %q, want hello", got)
	}
}

// TestFirstNonEmptyString_SkipsWhitespaceOnly returns first non-blank value.
func TestFirstNonEmptyString_SkipsWhitespaceOnly(t *testing.T) {
	got := firstNonEmptyString("   ", "  ", "third", "fourth")
	if got != "third" {
		t.Fatalf("got %q, want third", got)
	}
}

// TestFirstNonEmptyString_AllEmpty returns empty.
func TestFirstNonEmptyString_AllEmpty(t *testing.T) {
	got := firstNonEmptyString("", "  ", "\t")
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// TestIsJSONLikeValue_Nil returns false.
func TestIsJSONLikeValue_Nil(t *testing.T) {
	if isJSONLikeValue(nil) {
		t.Fatal("nil should not be JSON-like")
	}
}

// TestIsJSONLikeValue_Map returns true.
func TestIsJSONLikeValue_Map(t *testing.T) {
	if !isJSONLikeValue(map[string]any{}) {
		t.Fatal("map should be JSON-like")
	}
}

// TestIsJSONLikeValue_Slice returns true.
func TestIsJSONLikeValue_Slice(t *testing.T) {
	if !isJSONLikeValue([]int{}) {
		t.Fatal("slice should be JSON-like")
	}
}

// TestIsJSONLikeValue_Struct returns true.
func TestIsJSONLikeValue_Struct(t *testing.T) {
	type S struct{}
	if !isJSONLikeValue(S{}) {
		t.Fatal("struct should be JSON-like")
	}
}

// TestIsJSONLikeValue_Scalar returns false for scalars.
func TestIsJSONLikeValue_Scalar(t *testing.T) {
	if isJSONLikeValue("string") {
		t.Fatal("string should not be JSON-like")
	}
	if isJSONLikeValue(42) {
		t.Fatal("int should not be JSON-like")
	}
	if isJSONLikeValue(true) {
		t.Fatal("bool should not be JSON-like")
	}
}
