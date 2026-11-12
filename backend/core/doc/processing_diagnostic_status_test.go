package doc

import (
	"lazymind/core/common/orm"
	"testing"
)

func TestProcessingFailureIncludesRequiredEarlierStages(t *testing.T) {
	state := orm.DocumentProcessingState{ParseStatus: "failed", ChunkStatus: "pending", IndexStatus: "pending"}
	for _, level := range []string{"parsed", "chunked", "indexed"} {
		if got := DocumentProcessingStatus(level, state); got != "failed" {
			t.Fatalf("%s: %s", level, got)
		}
	}
	if got := DocumentProcessingStatus("stored", state); got != "not_requested" {
		t.Fatal(got)
	}
	state = orm.DocumentProcessingState{ParseStatus: "succeeded", ChunkStatus: "failed", IndexStatus: "pending"}
	if got := DocumentProcessingStatus("parsed", state); got != "ready" {
		t.Fatal(got)
	}
	if got := DocumentProcessingStatus("indexed", state); got != "failed" {
		t.Fatal(got)
	}
}

func TestDocumentBodyProcessingStatusIgnoresLaterStages(t *testing.T) {
	cases := []struct {
		name, observed, parse, chunk, index, want string
	}{
		{"not requested", ProcessingLevelStored, "pending", "pending", "pending", "not_requested"},
		{"parsed body ready while index pending", ProcessingLevelParsed, "succeeded", "pending", "pending", "ready"},
		{"body remains ready after index failure", ProcessingLevelIndexed, "succeeded", "succeeded", "failed", "ready"},
		{"current parse is pending", ProcessingLevelParsed, "pending", "pending", "pending", "pending"},
		{"current parse is running", ProcessingLevelParsed, "running", "pending", "pending", "running"},
		{"current parse failed", ProcessingLevelParsed, "failed", "pending", "pending", "failed"},
		{"submitted but no evidence", ProcessingLevelParsed, "", "", "", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := orm.DocumentProcessingState{
				ParseStatus: tc.parse,
				ChunkStatus: tc.chunk,
				IndexStatus: tc.index,
			}
			if got := DocumentBodyProcessingStatus(tc.observed, state); got != tc.want {
				t.Errorf("DocumentBodyProcessingStatus(%q, %+v) = %q, want %q", tc.observed, state, got, tc.want)
			}
		})
	}
}

func TestObservedDocumentProcessingStatusPrefersSubmittedLevel(t *testing.T) {
	state := orm.DocumentProcessingState{ParseStatus: "succeeded", ChunkStatus: "pending", IndexStatus: "pending"}
	cases := []struct {
		name, observed, want string
	}{
		{"submitted parsed level is ready", "parsed", "ready"},
		{"submitted level is trimmed", " parsed ", "ready"},
		{"empty observed level uses configured indexed level", "", "pending"},
		{"blank observed level uses configured indexed level", " \t", "pending"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ObservedDocumentProcessingStatus(tc.observed, "indexed", state); got != tc.want {
				t.Errorf("ObservedDocumentProcessingStatus(%q, %q, %+v) = %q, want %q", tc.observed, "indexed", state, got, tc.want)
			}
		})
	}
}
