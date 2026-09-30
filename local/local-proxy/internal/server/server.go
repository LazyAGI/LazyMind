package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/lazyagi/lazymind/local_proxy/internal/auth"
	"github.com/lazyagi/lazymind/local_proxy/internal/config"
)

func NewServer(cfg config.Config) *http.Server {
	return &http.Server{
		Addr:    cfg.ListenAddr(),
		Handler: NewHandler(cfg),
	}
}

func NewHandler(cfg config.Config) http.Handler {
	mux := http.NewServeMux()
	transport := &http.Transport{
		MaxConnsPerHost: 32, MaxIdleConnsPerHost: 32, IdleConnTimeout: 30 * time.Second,
		TLSHandshakeTimeout: cfg.Timeouts.Connect, ResponseHeaderTimeout: cfg.Timeouts.Read,
		ExpectContinueTimeout: cfg.Timeouts.Write, ForceAttemptHTTP2: true,
	}
	streamTransport := transport.Clone()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	workspaces := newWorkspaceHandler(cfg, client)
	mux.HandleFunc("/_local/healthz", healthz)
	mux.HandleFunc("/_local/status", func(w http.ResponseWriter, r *http.Request) {
		status(w, r, cfg)
	})
	mux.Handle("/_local/admin-session", &adminSessionHandler{
		cfg:     cfg,
		manager: auth.NewAdminSessionManager(cfg.Auth.AuthServiceURL, nil),
	})
	mux.HandleFunc("/_local/workspaces:select", workspaces.selectWorkspace)
	mux.HandleFunc("/_local/workspaces:authorize", workspaces.authorizeWorkspace)
	mux.HandleFunc("/_local/workspaces:reauthorize", workspaces.reauthorizeWorkspace)
	mux.Handle("/api/", &apiProxyHandler{
		routes:          cfg.Routes,
		rbac:            auth.NewRBACAdapter(cfg.Auth.AuthServiceURL, nil),
		transport:       transport,
		streamTransport: streamTransport,
		streamSlots:     make(chan struct{}, 32),
	})
	return newCORSHandler(mux, cfg.CORS.AllowedOrigins)
}

type apiProxyHandler struct {
	routes          []config.RouteConfig
	rbac            *auth.RBACAdapter
	transport       http.RoundTripper
	streamTransport http.RoundTripper
	streamSlots     chan struct{}
}

func (h *apiProxyHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	match, routeErr := MatchRoute(h.routes, req.URL.Path)
	if routeErr != nil {
		writeJSON(w, routeErr.StatusCode(), map[string]string{
			"error": routeErr.Error(),
		})
		return
	}

	realtimeUpgrade := req.Method == http.MethodGet && req.URL.EscapedPath() == "/api/core/realtime/connect" &&
		strings.EqualFold(req.Header.Get("Upgrade"), "websocket")
	if match.Route.Public || realtimeUpgrade {
		// Never forward caller-supplied identity across an unauthenticated route.
		req.Header.Del("X-User-Id")
		req.Header.Del("X-User-Name")
		req.Header.Del("X-Tenant-Id")
		req.Header.Del("X-User-Role")
	} else {
		if _, authErr := h.rbac.AuthorizeAndInjectIdentity(req.Context(), req); authErr != nil {
			payload := authErr.Body()
			if len(payload) == 0 {
				payload = map[string]string{"error": authErr.Error()}
			}
			writeJSON(w, authErr.Status, payload)
			return
		}
	}

	target, err := url.Parse(match.Upstream)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "Invalid upstream",
		})
		return
	}

	rewrittenPath := strings.TrimSuffix(target.Path, "/")
	if match.TargetPath == "/" {
		rewrittenPath += "/"
	} else {
		rewrittenPath += match.TargetPath
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = h.transport
	if h.streamTransport != nil && (strings.Contains(req.Header.Get("Accept"), "text/event-stream") ||
		strings.EqualFold(req.Header.Get("Upgrade"), "websocket")) {
		select {
		case h.streamSlots <- struct{}{}:
			defer func() { <-h.streamSlots }()
		default:
			w.Header().Set("Retry-After", "1")
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "realtime connection limit reached"})
			return
		}
		proxy.Transport = h.streamTransport
	}
	proxy.FlushInterval = -1
	proxy.ErrorHandler = h.errorHandler
	proxy.Director = func(outReq *http.Request) {
		outReq.URL.Scheme = target.Scheme
		outReq.URL.Host = target.Host
		outReq.URL.Path = rewrittenPath
		outReq.URL.RawPath = rewrittenPath
		outReq.Host = target.Host

		outReq.Header.Del("X-Forwarded-For")
		outReq.Header.Set("X-Forwarded-Host", req.Host)
		outReq.Header.Set("X-Forwarded-Proto", requestProto(req))
		outReq.Header.Set("X-Forwarded-For", req.RemoteAddr)
	}

	proxy.ServeHTTP(w, req)
}

func requestProto(req *http.Request) string {
	if req.TLS == nil {
		return "http"
	}
	return "https"
}

func (h *apiProxyHandler) errorHandler(w http.ResponseWriter, _ *http.Request, err error) {
	if isTimeoutError(err) {
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{
			"error": "upstream timeout",
		})
		return
	}
	if isConnectionError(err) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "upstream unavailable",
		})
		return
	}

	writeJSON(w, http.StatusBadGateway, map[string]string{
		"error": "upstream unavailable",
	})
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}

func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "connection refused") || strings.Contains(msg, "no such host") || strings.Contains(msg, "network is unreachable") {
		return true
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
