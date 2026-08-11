package main

import (
	"net/http"
	"strings"
	"time"

	"lazymind/core/log"
)

type auditResponseWriter struct {
	http.ResponseWriter
	statusCode int
	bytesSent  int64
}

func newAuditResponseWriter(w http.ResponseWriter) *auditResponseWriter {
	return &auditResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
}

func (w *auditResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *auditResponseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytesSent += int64(n)
	return n, err
}

func withMutationRequestAudit(routeMethod, routePath string, next http.HandlerFunc) http.HandlerFunc {
	if !shouldAuditBackendMutationRequest(routeMethod, routePath) {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Logger.Info().
			Str("audit_type", "request_in").
			Str("route_method", routeMethod).
			Str("route_path", routePath).
			Str("request_method", r.Method).
			Str("request_path", r.URL.Path).
			Str("remote_addr", r.RemoteAddr).
			Int64("content_length", r.ContentLength).
			Msg("backend mutation request received")

		recorder := newAuditResponseWriter(w)
		next(recorder, r)

		log.Logger.Info().
			Str("audit_type", "request_out").
			Str("route_method", routeMethod).
			Str("route_path", routePath).
			Str("request_method", r.Method).
			Str("request_path", r.URL.Path).
			Int("status_code", recorder.statusCode).
			Int64("bytes_sent", recorder.bytesSent).
			Dur("duration", time.Since(start)).
			Msg("backend mutation request completed")
	}
}

func shouldAuditBackendMutationRequest(method, path string) bool {
	method = strings.ToUpper(strings.TrimSpace(method))
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return false
	}

	path = strings.TrimSpace(path)
	switch {
	case strings.HasPrefix(path, "/skill/"):
		return true
	case path == "/skills":
		return true
	case strings.HasPrefix(path, "/skills/"):
		return true
	default:
		return false
	}
}
