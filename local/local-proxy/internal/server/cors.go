package server

import (
	"net/http"
	"net/url"
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

	_, explicitlyAllowed := h.allowedOrigin[origin]
	if !explicitlyAllowed && !browserExtensionOriginAllowed(req.URL.Path, origin) {
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

func browserExtensionOriginAllowed(path, origin string) bool {
	if path != "/api/browser/v1" && !strings.HasPrefix(path, "/api/browser/v1/") {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || strings.TrimSpace(parsed.Host) == "" {
		return false
	}
	return parsed.Scheme == "chrome-extension" || parsed.Scheme == "edge-extension"
}
