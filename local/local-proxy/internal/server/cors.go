package server

import (
	"net/http"
	"strings"
)

const (
	corsMaxAgeSeconds = "600"
)

type corsHandler struct {
	next          http.Handler
	allowedOrigin map[string]struct{}
}

func newCORSHandler(next http.Handler, allowedOrigins []string) http.Handler {
	allowedMap := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}
		allowedMap[origin] = struct{}{}
	}

	return &corsHandler{
		next:          next,
		allowedOrigin: allowedMap,
	}
}

func (h *corsHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	origin := strings.TrimSpace(req.Header.Get("Origin"))
	if origin == "" {
		h.next.ServeHTTP(w, req)
		return
	}

	if _, ok := h.allowedOrigin[origin]; !ok {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "origin not allowed",
		})
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Credentials", "true")

	if req.Method == http.MethodOptions {
		requestedMethod := strings.TrimSpace(req.Header.Get("Access-Control-Request-Method"))
		if requestedMethod == "" {
			requestedMethod = http.MethodGet
		}
		w.Header().Set("Access-Control-Allow-Methods", requestedMethod)
		if requestedHeaders := strings.TrimSpace(req.Header.Get("Access-Control-Request-Headers")); requestedHeaders != "" {
			w.Header().Set("Access-Control-Allow-Headers", requestedHeaders)
		}
		w.Header().Set("Access-Control-Max-Age", corsMaxAgeSeconds)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	h.next.ServeHTTP(w, req)
}
