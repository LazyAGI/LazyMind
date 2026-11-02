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
