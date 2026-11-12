package common

import "net/http"

func init() {
	registerAdditionalErrorAlias("update_transaction_busy", "Conflict", http.StatusConflict, ErrCodeConflict)
	for _, source := range []string{
		"component_install_failed",
		"component_state_unknown",
	} {
		registerAdditionalErrorAlias(source, "Internal server error", http.StatusServiceUnavailable, ErrCodeInternal)
	}
	for _, source := range []string{
		"component_file_invalid",
		"component_path_invalid",
		"component_target_verification_failed",
		"update_bootstrap_invalid",
	} {
		registerAdditionalErrorAlias(source, "Internal server error", http.StatusInternalServerError, ErrCodeInternal)
	}
}
