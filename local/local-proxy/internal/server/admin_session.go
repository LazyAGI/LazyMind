package server

import (
	"net"
	"net/http"
	"strings"

	"github.com/lazyagi/lazymind/local_proxy/internal/auth"
	"github.com/lazyagi/lazymind/local_proxy/internal/config"
)

type adminSessionHandler struct {
	cfg     config.Config
	manager *auth.AdminSessionManager
}

func (h *adminSessionHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if !h.cfg.Auth.AutoLoginAllowLAN && !requestFromLoopback(req) {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "local admin auto-login is only available from this machine",
		})
		return
	}

	session, err := h.manager.Ensure(req.Context(), forceAdminSessionRefresh(req))
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "admin session unavailable",
		})
		return
	}

	writeJSON(w, http.StatusOK, session)
}

func forceAdminSessionRefresh(req *http.Request) bool {
	switch strings.ToLower(strings.TrimSpace(req.URL.Query().Get("force"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func requestFromLoopback(req *http.Request) bool {
	if !isLoopbackHost(trimHostPort(req.RemoteAddr)) {
		return false
	}

	if forwardedFor := strings.TrimSpace(req.Header.Get("X-Forwarded-For")); forwardedFor != "" {
		parts := strings.Split(forwardedFor, ",")
		last := strings.TrimSpace(parts[len(parts)-1])
		return isLoopbackHost(trimHostPort(last))
	}
	if realIP := strings.TrimSpace(req.Header.Get("X-Real-IP")); realIP != "" {
		return isLoopbackHost(trimHostPort(realIP))
	}
	return true
}

func isLoopbackHost(value string) bool {
	if value == "localhost" {
		return true
	}
	ip := net.ParseIP(value)
	return ip != nil && ip.IsLoopback()
}

func trimHostPort(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(value)
	if err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(value, "[]")
}
