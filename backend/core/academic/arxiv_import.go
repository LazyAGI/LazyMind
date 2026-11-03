package academic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"lazymind/core/academic/arxivsource"
)

func replyArxivImportError(w http.ResponseWriter, err error) {
	code, status := "ARXIV_IMPORT_FAILED", http.StatusInternalServerError
	var upstream *arxivsource.LookupError
	if errors.As(err, &upstream) {
		code = "ARXIV_" + strings.ToUpper(upstream.Code)
		status = upstream.StatusCode
		switch upstream.Code {
		case "invalid_input":
			status = http.StatusBadRequest
		case "not_found":
			status = http.StatusNotFound
		}
		if status < 400 || status > 599 {
			status = 502
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		code, status = "ARXIV_TIMEOUT", http.StatusGatewayTimeout
	}
	if errors.Is(err, context.Canceled) {
		code, status = "ARXIV_CANCELLED", http.StatusRequestTimeout
	}
	body := map[string]any{"code": code, "message": err.Error()}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
