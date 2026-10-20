package common

import "net/http"

func init() {
	for _, source := range []string{
		"WORKFLOW_ARTIFACT_NOT_FROZEN",
		"WORKFLOW_PUBLICATION_INPUT_CHANGED",
		"WORKFLOW_PUBLICATION_INCOMPLETE",
		// Keep domain/older-server errors resolvable during the publication refactor.
		"PRODUCT_ARTIFACT_NOT_FROZEN",
		"PRODUCT_PUBLICATION_INPUT_CHANGED",
		"PRODUCT_PUBLICATION_INCOMPLETE",
		"PRODUCT_PUBLICATION_CONTENT_MISMATCH",
	} {
		registerAdditionalErrorAlias(source, "Conflict", http.StatusConflict, 2000107)
	}
	for _, source := range []string{"WORKFLOW_PUBLICATION_INVALID", "PRODUCT_ARTIFACT_INVALID", "PRODUCT_PUBLICATION_INVALID"} {
		registerAdditionalErrorAlias(source, "Invalid request", http.StatusBadRequest, 2000103)
	}
}
