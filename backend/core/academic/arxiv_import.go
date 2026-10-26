package academic

import (
	"context"
	"encoding/json"
	"errors"
	"lazymind/core/academic/arxivsource"
	"net/http"
	"strings"
	"time"
)

func LookupArxiv(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUser(w, r); !ok {
		return
	}
	var req struct {
		Input string `json:"input"`
	}
	if !parseJSON(r, &req) || strings.TrimSpace(req.Input) == "" {
		replyError(w, 400, "INVALID_REQUEST", "请输入 arXiv 编号或链接")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	paper, err := arxivsource.NewClient().Lookup(ctx, req.Input)
	if err != nil {
		replyArxivImportError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"paper": paper})
}

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
