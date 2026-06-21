package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lazyagi/lazymind/local_proxy/internal/config"
)

var statusRoundTripper http.RoundTripper

func healthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":    "ok",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

type localRouteStatus struct {
	Name         string `json:"name"`
	Prefix       string `json:"prefix"`
	Enabled      bool   `json:"enabled"`
	Optional     bool   `json:"optional"`
	HealthPath   string `json:"healthPath"`
	HealthStatus string `json:"health"`
	HealthCode   int    `json:"healthCode"`
	HealthError  string `json:"healthError,omitempty"`
	Upstream     string `json:"upstream"`
}

type localStatusResponse struct {
	Status    string             `json:"status"`
	Timestamp string             `json:"timestamp"`
	Listen    listenInfo         `json:"listen"`
	AuthMode  string             `json:"authMode"`
	Routes    []localRouteStatus `json:"routes"`
}

type listenInfo struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

const localStatusHealthTimeout = 2 * time.Second

func status(w http.ResponseWriter, r *http.Request, cfg config.Config) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	client := &http.Client{
		Timeout:   localStatusHealthTimeout,
		Transport: statusHTTPTransport(),
	}

	routeStatuses := make([]localRouteStatus, 0, len(cfg.Routes))
	for _, route := range cfg.Routes {
		routeStatuses = append(routeStatuses, routeStatus(route, client))
	}

	writeJSON(w, http.StatusOK, localStatusResponse{
		Status:    "ok",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Listen: listenInfo{
			Host: cfg.Listen.Host,
			Port: cfg.Listen.Port,
		},
		AuthMode: cfg.Auth.Mode,
		Routes:   routeStatuses,
	})
}

func statusHTTPTransport() http.RoundTripper {
	if statusRoundTripper != nil {
		return statusRoundTripper
	}
	statusRoundTripper = &http.Transport{
		MaxConnsPerHost:       16,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       5 * time.Second,
		TLSHandshakeTimeout:   2 * time.Second,
		ResponseHeaderTimeout: 2 * time.Second,
		ExpectContinueTimeout: 2 * time.Second,
	}
	return statusRoundTripper
}

func routeStatus(route config.RouteConfig, client *http.Client) localRouteStatus {
	output := localRouteStatus{
		Name:       route.Name,
		Prefix:     route.Prefix,
		Enabled:    route.Enabled,
		Optional:   route.Optional,
		Upstream:   sanitizeUpstream(route.Upstream),
		HealthPath: route.HealthPath,
	}

	if !route.Enabled {
		output.HealthStatus = "disabled"
		return output
	}

	healthPath := strings.TrimSpace(route.HealthPath)
	if healthPath == "" {
		output.HealthStatus = "unknown"
		output.HealthError = "no health path configured"
		return output
	}

	if !strings.HasPrefix(healthPath, "/") {
		output.HealthStatus = "unknown"
		output.HealthError = "invalid health path"
		return output
	}

	healthURL, err := healthEndpointURL(route.Upstream, healthPath)
	if err != nil {
		output.HealthStatus = "unknown"
		output.HealthError = "invalid upstream URL"
		return output
	}

	req, err := http.NewRequest(http.MethodGet, healthURL.String(), nil)
	if err != nil {
		output.HealthStatus = "unknown"
		output.HealthError = "failed to build request"
		return output
	}
	res, err := client.Do(req)
	if err != nil {
		output.HealthStatus = "unreachable"
		output.HealthError = "upstream unavailable"
		return output
	}
	defer res.Body.Close()

	output.HealthCode = res.StatusCode
	if res.StatusCode >= http.StatusOK && res.StatusCode < http.StatusMultipleChoices {
		output.HealthStatus = "healthy"
		return output
	}

	output.HealthStatus = "unhealthy"
	output.HealthError = fmt.Sprintf("status=%d", res.StatusCode)
	return output
}

func sanitizeUpstream(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "invalid"
	}
	return (&url.URL{
		Scheme: u.Scheme,
		Host:   u.Host,
	}).String()
}

func healthEndpointURL(upstream, path string) (*url.URL, error) {
	base, err := url.Parse(upstream)
	if err != nil {
		return nil, err
	}

	healthURL := base.ResolveReference(&url.URL{Path: path})
	if healthURL.Path == "" {
		return nil, fmt.Errorf("invalid health path")
	}
	return healthURL, nil
}
