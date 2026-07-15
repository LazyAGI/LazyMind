package handler

import (
	"testing"

	skillrevision "lazymind/core/skillv2/revision"
)

func TestRevisionDTOIncludesHeadMarker(t *testing.T) {
	dto := revisionDTO(skillrevision.Revision{ID: "rev1", RevisionID: "rev1", IsHead: true})
	if got, ok := dto["is_head"].(bool); !ok || !got {
		t.Fatalf("is_head = %#v, want true", dto["is_head"])
	}
}
