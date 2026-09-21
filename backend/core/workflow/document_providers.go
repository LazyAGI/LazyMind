package workflow

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"lazymind/core/algo"
	"lazymind/core/common"
	corestore "lazymind/core/store"
)

func ListDocumentProviders(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(corestore.UserID(r)) == "" {
		replyDocumentFailure(w, documentFailure("IDENTITY_REQUIRED", 400))
		return
	}
	if r.Header.Get("X-LazyMind-External-Ref") != "" {
		replyDocumentFailure(w, documentFailure("PERMISSION_DENIED", 403))
		return
	}
	if r.URL.RawQuery != "" {
		replyDocumentFailure(w, documentFailure("DOCUMENT_PROVIDERS_INVALID", 400))
		return
	}
	if r.Body != nil {
		// This GET has no payload. Read at most one byte, including chunked bodies.
		body, err := io.ReadAll(io.LimitReader(r.Body, 1))
		if err != nil || len(body) != 0 {
			replyDocumentFailure(w, documentFailure("DOCUMENT_PROVIDERS_INVALID", 400))
			return
		}
	}
	catalog, err := algo.ListDocumentProviders(r.Context())
	if err != nil {
		code := "DOCUMENT_PROVIDERS_UNAVAILABLE"
		var syntax *json.SyntaxError
		var invalidType *json.UnmarshalTypeError
		if errors.As(err, &syntax) || errors.As(err, &invalidType) {
			code = "DOCUMENT_PROVIDERS_RESULT_INVALID"
		}
		replyDocumentFailure(w, documentFailure(code, 502))
		return
	}
	if !validDocumentProviderCatalog(catalog) {
		replyDocumentFailure(w, documentFailure("DOCUMENT_PROVIDERS_RESULT_INVALID", 502))
		return
	}
	if r.Context().Err() != nil {
		replyDocumentFailure(w, documentFailure("DOCUMENT_PROVIDERS_UNAVAILABLE", 502))
		return
	}
	// Only the declared DTO fields are projected. Unknown upstream metadata does
	// not become credential/sync policy or publication eligibility.
	common.ReplyOK(w, catalog)
}
func validDocumentProviderCatalog(catalog *algo.DocumentProviderCatalog) bool {
	if catalog == nil || catalog.Providers == nil {
		return false
	}
	ids := make(map[string]struct{}, len(catalog.Providers))
	for _, provider := range catalog.Providers {
		if provider == nil || strings.TrimSpace(provider.ID) == "" || provider.Capabilities == nil {
			return false
		}
		if _, duplicate := ids[provider.ID]; duplicate {
			return false
		}
		ids[provider.ID] = struct{}{}
		capabilities := make(map[string]struct{}, len(provider.Capabilities))
		for _, capability := range provider.Capabilities {
			if strings.TrimSpace(capability) == "" {
				return false
			}
			if _, duplicate := capabilities[capability]; duplicate {
				return false
			}
			capabilities[capability] = struct{}{}
		}
	}
	return true
}
