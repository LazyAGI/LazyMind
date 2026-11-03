package coreadapter

import (
	"lazymind/core/doc"
	"testing"
)

func TestOperationMappingPreservesUnconfirmedCount(t *testing.T) {
	source := doc.ResourceOperationResult{Action: "knowledge.resource.move", Status: "partial", Summary: doc.ResourceOperationSummary{Total: 2, Succeeded: 1, Unconfirmed: 1}, Items: []doc.ResourceOperationItem{{ResourceStatus: "unconfirmed", Error: &doc.ResourceError{Code: "UNCONFIRMED", Message: "check receipt", Details: map[string]any{"operation_key": "stable"}}}}}
	mapped, err := mapOperation(source.Action, source, nil)
	if err != nil || mapped.Summary.Unconfirmed != 1 || mapped.Items[0].Error.Code != "UNCONFIRMED" || mapped.Items[0].Error.Details["operation_key"] != "stable" {
		t.Fatalf("lost uncertainty: %+v %v", mapped, err)
	}
}
