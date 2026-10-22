package sourceurl

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	githubJSONLimit    = 8 << 20
	githubCacheBytes   = 16 << 20
	githubCacheEntries = 64
	githubCacheTTL     = 30 * time.Second
	githubMaxRetryWait = 2 * time.Second
)

type githubAPIResult struct {
	status int
	body   []byte
	err    error
}

type githubAPICacheEntry struct {
	result  githubAPIResult
	expires time.Time
}

type githubAPIFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	result  githubAPIResult
}

type githubAPICacheKey struct {
	endpoint  string
	tokenHash [32]byte
	// Custom clients may route the same URL to different origins. Holding the
	// client keeps its identity stable for the lifetime of a cached result.
	client *http.Client
}

var githubAPIMetadata = struct {
	sync.Mutex
	cache   map[githubAPICacheKey]githubAPICacheEntry
	flights map[githubAPICacheKey]*githubAPIFlight
	bytes   int
}{cache: make(map[githubAPICacheKey]githubAPICacheEntry), flights: make(map[githubAPICacheKey]*githubAPIFlight)}

// GitHubAPIGet fetches bounded JSON metadata, reusing successful responses for a
// short period and coalescing concurrent requests. Optional environment-token
// authentication is sent only to HTTPS api.github.com, including on redirects.
// Each caller may cancel independently of other callers sharing its request.
func GitHubAPIGet(ctx context.Context, client *http.Client, endpoint string, out any) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return 0, err
	}
	token := ""
	if githubTokenHost(parsed) {
		token = strings.TrimSpace(os.Getenv("LAZYMIND_GITHUB_TOKEN"))
		if token == "" {
			token = strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
		}
	}
	// Authentication affects metadata visibility. Keep responses from different
	// credentials isolated without storing the token itself as a cache key.
	key := githubAPICacheKey{endpoint: endpoint, tokenHash: sha256.Sum256([]byte(token))}
	if client != nil && client.Transport != nil {
		key.client = client
	}
	githubAPIMetadata.Lock()
	if entry, ok := githubAPIMetadata.cache[key]; ok && time.Now().Before(entry.expires) {
		githubAPIMetadata.Unlock()
		return decodeGitHubAPIResult(entry.result, out)
	}
	flight := githubAPIMetadata.flights[key]
	if flight == nil {
		requestCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		flight = &githubAPIFlight{done: make(chan struct{}), cancel: cancel}
		githubAPIMetadata.flights[key] = flight
		go func() {
			result := fetchGitHubAPI(requestCtx, client, endpoint, token)
			cancel()
			githubAPIMetadata.Lock()
			flight.result = result
			if githubAPIMetadata.flights[key] == flight {
				delete(githubAPIMetadata.flights, key)
				if result.err == nil && result.status >= 200 && result.status < 300 && len(result.body) > 0 {
					cacheGitHubAPIResult(key, result)
				}
			}
			close(flight.done)
			githubAPIMetadata.Unlock()
		}()
	}
	flight.waiters++
	githubAPIMetadata.Unlock()
	defer func() {
		githubAPIMetadata.Lock()
		flight.waiters--
		if flight.waiters == 0 {
			flight.cancel()
			// A later caller must not join an abandoned, canceled request.
			if githubAPIMetadata.flights[key] == flight {
				delete(githubAPIMetadata.flights, key)
			}
		}
		githubAPIMetadata.Unlock()
	}()
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-flight.done:
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return decodeGitHubAPIResult(flight.result, out)
	}
}

func githubAPIGet(ctx context.Context, client *http.Client, endpoint string, out any) (int, error) {
	return GitHubAPIGet(ctx, client, endpoint, out)
}

// Caller holds githubAPIMetadata's mutex.
func cacheGitHubAPIResult(key githubAPICacheKey, result githubAPIResult) {
	now := time.Now()
	for candidate, entry := range githubAPIMetadata.cache {
		if !now.Before(entry.expires) {
			delete(githubAPIMetadata.cache, candidate)
			githubAPIMetadata.bytes -= len(entry.result.body)
		}
	}
	for len(githubAPIMetadata.cache) >= githubCacheEntries || githubAPIMetadata.bytes+len(result.body) > githubCacheBytes {
		var oldestKey githubAPICacheKey
		var oldest time.Time
		for candidate, entry := range githubAPIMetadata.cache {
			if oldestKey.endpoint == "" || entry.expires.Before(oldest) {
				oldestKey, oldest = candidate, entry.expires
			}
		}
		if oldestKey.endpoint == "" {
			break
		}
		githubAPIMetadata.bytes -= len(githubAPIMetadata.cache[oldestKey].result.body)
		delete(githubAPIMetadata.cache, oldestKey)
	}
	githubAPIMetadata.cache[key] = githubAPICacheEntry{result: result, expires: now.Add(githubCacheTTL)}
	githubAPIMetadata.bytes += len(result.body)
}

func decodeGitHubAPIResult(result githubAPIResult, out any) (int, error) {
	if result.err != nil {
		return result.status, result.err
	}
	if out != nil && result.status >= 200 && result.status < 300 {
		if err := json.Unmarshal(result.body, out); err != nil {
			return result.status, fmt.Errorf("GitHub API returned invalid JSON: %w", err)
		}
	}
	return result.status, nil
}

func githubTokenHost(parsed *url.URL) bool {
	return parsed != nil && parsed.Scheme == "https" && strings.EqualFold(parsed.Host, "api.github.com") && parsed.User == nil
}

func fetchGitHubAPI(ctx context.Context, client *http.Client, endpoint, token string) githubAPIResult {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	// Clone before constraining redirects, preserving the supplied transport and
	// redirect policy without mutating a client that other requests may share.
	safeClient := *client
	if safeClient.Timeout == 0 {
		safeClient.Timeout = 10 * time.Second
	}
	originalRedirect := safeClient.CheckRedirect
	safeClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !githubTokenHost(req.URL) {
			req.Header.Del("Authorization")
		}
		if originalRedirect != nil {
			err := originalRedirect(req, via)
			if !githubTokenHost(req.URL) {
				req.Header.Del("Authorization")
			}
			return err
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return githubAPIResult{err: err}
		}
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("User-Agent", "LazyMind-Skill-Importer")
		if token != "" && githubTokenHost(request.URL) {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := safeClient.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return githubAPIResult{err: ctx.Err()}
			}
			if attempt < 2 {
				if err := waitGitHubRetry(ctx, time.Duration(attempt+1)*100*time.Millisecond); err != nil {
					return githubAPIResult{err: err}
				}
				continue
			}
			return githubAPIResult{err: fmt.Errorf("GitHub API request failed: %w", err)}
		}
		limit := int64(githubJSONLimit)
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			limit = 64 << 10
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
		response.Body.Close()
		result := githubAPIResult{status: response.StatusCode, body: body}
		if readErr != nil {
			return githubAPIResult{status: result.status, err: fmt.Errorf("GitHub API response could not be read: %w", readErr)}
		}
		if result.status >= 200 && result.status < 300 {
			if len(body) > githubJSONLimit {
				result.err = fmt.Errorf("GitHub API response exceeds 8 MiB")
			} else if len(body) > 0 && !json.Valid(body) {
				result.err = fmt.Errorf("GitHub API returned invalid JSON")
			}
			return result
		}
		rateLimited := result.status == http.StatusTooManyRequests || (result.status == http.StatusForbidden && (response.Header.Get("X-RateLimit-Remaining") == "0" || response.Header.Get("Retry-After") != "" || strings.Contains(strings.ToLower(string(body)), "rate limit")))
		delay, explicit := githubRetryDelay(response.Header)
		if rateLimited {
			result.err = githubRateLimitError(response.Header)
			if !explicit {
				return result
			}
		}
		transient := rateLimited || result.status == http.StatusInternalServerError || result.status == http.StatusBadGateway || result.status == http.StatusServiceUnavailable || result.status == http.StatusGatewayTimeout
		if !transient || attempt == 2 || delay > githubMaxRetryWait {
			return result
		}
		if !explicit {
			delay = time.Duration(attempt+1) * 100 * time.Millisecond
		}
		if err := waitGitHubRetry(ctx, delay); err != nil {
			return githubAPIResult{status: result.status, err: err}
		}
	}
	panic("unreachable")
}

func githubRetryDelay(header http.Header) (time.Duration, bool) {
	if raw := header.Get("Retry-After"); raw != "" {
		if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds >= 0 {
			if seconds > 3600 {
				return time.Hour, true
			}
			return time.Duration(seconds) * time.Second, true
		}
		if date, err := http.ParseTime(raw); err == nil {
			return max(time.Until(date), 0), true
		}
	}
	if header.Get("X-RateLimit-Remaining") == "0" {
		if seconds, err := strconv.ParseInt(header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			return max(time.Until(time.Unix(seconds, 0)), 0), true
		}
	}
	return 0, false
}

func githubRateLimitError(header http.Header) error {
	if raw := header.Get("Retry-After"); raw != "" {
		return fmt.Errorf("GitHub API rate limit reached; retry after %s (Retry-After)", raw)
	}
	if raw := header.Get("X-RateLimit-Reset"); raw != "" {
		if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return fmt.Errorf("GitHub API rate limit reached; retry after %s", time.Unix(seconds, 0).UTC().Format(time.RFC3339))
		}
	}
	return fmt.Errorf("GitHub API rate limit reached; retry in at least one minute, or configure LAZYMIND_GITHUB_TOKEN for authenticated requests")
}

func waitGitHubRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
