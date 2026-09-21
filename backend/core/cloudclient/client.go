package cloudclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const reachabilityTimeout = 2 * time.Second

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
}

func New(baseURL string, httpClient *http.Client) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("invalid LazyMind Cloud base URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("LazyMind Cloud base URL must be an origin")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname())) {
		return nil, errors.New("LazyMind Cloud base URL must use HTTPS")
	}
	parsed.Path = ""
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{baseURL: parsed, httpClient: httpClient}, nil
}

func (c *Client) Origin() string {
	if c == nil || c.baseURL == nil {
		return ""
	}
	return c.baseURL.String()
}

// CheckReachability verifies only that the configured Cloud HTTPS origin is
// serving its fixed health endpoint. It deliberately sends no credentials and
// does not imply that a Desktop user is signed in or entitled to any feature.
func (c *Client) CheckReachability(ctx context.Context) error {
	if c == nil || c.baseURL == nil || c.httpClient == nil {
		return errors.New("LazyMind Cloud is not configured")
	}
	probeCtx, cancel := context.WithTimeout(ctx, reachabilityTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, c.resolve("/healthz"), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	client := *c.httpClient
	previousRedirect := client.CheckRedirect
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if next.URL.Scheme != c.baseURL.Scheme || next.URL.Host != c.baseURL.Host {
			return errors.New("LazyMind Cloud health check redirected outside its origin")
		}
		if previousRedirect != nil {
			return previousRedirect(next, via)
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("LazyMind Cloud health status %d", response.StatusCode)
	}
	read, err := io.Copy(io.Discard, io.LimitReader(response.Body, 4097))
	if err != nil {
		return err
	}
	if read > 4096 {
		return errors.New("LazyMind Cloud health response is too large")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c *Client) resolve(path string) string {
	copy := *c.baseURL
	copy.Path = strings.TrimRight(c.baseURL.Path, "/") + "/" + strings.TrimLeft(path, "/")
	copy.RawQuery = ""
	copy.Fragment = ""
	return copy.String()
}
