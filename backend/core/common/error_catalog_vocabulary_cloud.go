package common

import "net/http"

func init() {
	for _, group := range []struct {
		code    int
		status  int
		message string
		sources []string
	}{
		{2005300, http.StatusBadRequest, "Invalid vocabulary sync configuration", []string{"invalid vocabulary cloud request", "invalid vocabulary cloud provider", "invalid vocabulary cloud target", "invalid vocabulary cloud operation", "cloud vocabulary: invalid_configuration", "cloud vocabulary: invalid_request", "cloud vocabulary: invalid_name", "cloud vocabulary: invalid_word"}},
		{2005301, http.StatusUnauthorized, "Vocabulary service authorization failed; check the token", []string{"invalid vocabulary cloud token", "cloud vocabulary: invalid_token"}},
		{2005302, http.StatusServiceUnavailable, "Vocabulary credentials are unavailable on this device", []string{"vocabulary cloud credentials unavailable"}},
		{2005303, http.StatusConflict, "Vocabulary connection is disabled or missing", []string{"vocabulary cloud connection disabled", "vocabulary cloud connection not found"}},
		{2005304, http.StatusTooManyRequests, "Vocabulary service rate limit reached; sync will retry later", []string{"cloud vocabulary: rate_limited"}},
		{2005305, http.StatusBadGateway, "Vocabulary sync failed; your local word is preserved", []string{"vocabulary cloud sync failed", "cloud vocabulary: request_failed", "cloud vocabulary: request_cancelled", "cloud vocabulary: response_failed", "cloud vocabulary: upstream_rejected", "cloud vocabulary: invalid_response", "cloud vocabulary: response_too_large", "cloud vocabulary: too_many_wordbooks"}},
		{2005306, http.StatusBadRequest, "The word is unavailable in the remote dictionary", []string{"cloud vocabulary: word_not_found"}},
		{2005307, http.StatusConflict, "The word was not added to the study plan; check the remote account quota", []string{"cloud vocabulary: study_word_not_added"}},
		{2005308, http.StatusInternalServerError, "Vocabulary sync state could not be saved", []string{"vocabulary cloud storage failed"}},
	} {
		for _, source := range group.sources {
			registerAdditionalErrorAlias(source, group.message, group.status, group.code)
		}
	}
}
