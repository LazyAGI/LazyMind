package vocabulary

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type CloudWordbook struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CloudClientError deliberately contains no upstream response, URL or credential.
type CloudClientError struct {
	Code       string
	Retryable  bool
	RetryAfter time.Duration
}

func (e *CloudClientError) Error() string { return "cloud vocabulary: " + e.Code }
func cloudError(code string, retry bool) *CloudClientError {
	return &CloudClientError{Code: code, Retryable: retry}
}

type cloudClient struct {
	provider, token string
	httpClient      *http.Client
	limiter         *cloudRateLimit
}
type cloudRateLimit struct {
	mu        sync.Mutex
	next      time.Time
	interval  time.Duration
	operation chan struct{}
}

var cloudRateLimits sync.Map

// cloudAccountKey scopes account serialization without persisting credentials.
func cloudAccountKey(provider, token string) string {
	token = strings.TrimSpace(token)
	if provider == "maimemo" {
		token = strings.TrimSpace(strings.TrimPrefix(token, "Bearer "))
	}
	digest := sha256.Sum256([]byte(provider + "\x00" + token))
	return hex.EncodeToString(digest[:])
}

// lockOperation protects the complete read/merge/write sequence across clients.
func (l *cloudRateLimit) lockOperation(ctx context.Context) (func(), error) {
	if l == nil {
		return func() {}, nil
	}
	l.mu.Lock()
	if l.operation == nil {
		l.operation = make(chan struct{}, 1)
	}
	ch := l.operation
	l.mu.Unlock()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, cloudError("request_cancelled", true)
	}
}
func newCloudClient(provider, token string) (*cloudClient, error) {
	token = strings.TrimSpace(token)
	if (provider != "eudic" && provider != "maimemo") || token == "" || strings.ContainsAny(token, "\r\n") {
		return nil, cloudError("invalid_configuration", false)
	}
	interval := 2100 * time.Millisecond
	if provider == "maimemo" {
		interval = 1600 * time.Millisecond
	}
	key := cloudAccountKey(provider, token)
	v, _ := cloudRateLimits.LoadOrStore(key, &cloudRateLimit{interval: interval})
	return &cloudClient{provider: provider, token: token, limiter: v.(*cloudRateLimit), httpClient: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (l *cloudRateLimit) wait(ctx context.Context) error {
	if l == nil {
		return nil
	}
	for {
		l.mu.Lock()
		delay := time.Until(l.next)
		if delay <= 0 {
			l.next = time.Now().Add(l.interval)
			l.mu.Unlock()
			return nil
		}
		l.mu.Unlock()
		if delay > 5*time.Second {
			return &CloudClientError{Code: "rate_limited", Retryable: true, RetryAfter: delay}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (l *cloudRateLimit) cool(d time.Duration) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if next := time.Now().Add(d); next.After(l.next) {
		l.next = next
	}
}
func (c *cloudClient) request(ctx context.Context, method, path string, payload any, out any) error {
	if err := c.limiter.wait(ctx); err != nil {
		var safe *CloudClientError
		if errors.As(err, &safe) {
			return safe
		}
		return cloudError("request_cancelled", true)
	}
	base := "https://api.frdic.com/api/open/v1/studylist"
	auth := c.token
	if c.provider == "maimemo" {
		base = "https://open.maimemo.com/open/api/v1/memo"
		auth = "Bearer " + strings.TrimPrefix(c.token, "Bearer ")
	}
	var body io.Reader
	if payload != nil {
		b, e := json.Marshal(payload)
		if e != nil {
			return cloudError("invalid_request", false)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return cloudError("invalid_request", false)
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := c.httpClient.Do(req)
	if err != nil {
		return cloudError("request_failed", true)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		retry := res.StatusCode == 429 || res.StatusCode == 408 || res.StatusCode >= 500 || (c.provider == "eudic" && res.StatusCode == 403)
		e := cloudError("upstream_rejected", retry)
		if res.StatusCode == 401 {
			e.Code = "invalid_token"
		}
		if res.StatusCode == 429 || res.StatusCode == 403 {
			e.Code = "rate_limited"
		}
		if sec, x := strconv.Atoi(res.Header.Get("Retry-After")); x == nil && sec > 0 {
			e.RetryAfter = time.Duration(sec) * time.Second
		} else if at, x := http.ParseTime(res.Header.Get("Retry-After")); x == nil && at.After(time.Now()) {
			e.RetryAfter = time.Until(at)
		}
		if c.provider == "eudic" && res.StatusCode == 403 && e.RetryAfter < time.Hour {
			e.RetryAfter = time.Hour
		}
		if e.RetryAfter > 0 {
			c.limiter.cool(e.RetryAfter)
		}
		return e
	}
	if c.provider == "eudic" && method == "POST" && res.StatusCode != http.StatusCreated {
		return cloudError("invalid_response", false)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024+1))
	if err != nil {
		return cloudError("response_failed", true)
	}
	if len(b) > 2*1024*1024 {
		return cloudError("response_too_large", false)
	}
	var env struct {
		Data    json.RawMessage   `json:"data"`
		Success *bool             `json:"success"`
		Errors  []json.RawMessage `json:"errors"`
	}
	if json.Unmarshal(b, &env) != nil {
		return cloudError("invalid_response", false)
	}
	if (env.Success != nil && !*env.Success) || len(env.Errors) > 0 {
		return cloudError("upstream_rejected", false)
	}
	if c.provider == "maimemo" {
		if len(env.Data) > 0 {
			b = env.Data
		}
	} else if out != nil {
		b = env.Data
	}
	if out != nil && (len(b) == 0 || string(b) == "null" || json.Unmarshal(b, out) != nil) {
		return cloudError("invalid_response", false)
	}
	return nil
}
func (c *cloudClient) ListWordbooks(ctx context.Context) ([]CloudWordbook, error) {
	if c.provider == "eudic" {
		var books []CloudWordbook
		e := c.request(ctx, "GET", "/category?language=en", nil, &books)
		return books, e
	}
	books := []CloudWordbook{}
	// MaiMemo rejects requests with a limit greater than 10.
	const pageSize = 10
	for offset := 0; offset < 10000; offset += pageSize {
		var data struct {
			Notepads []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"notepads"`
		}
		if e := c.request(ctx, "GET", "/notepads?limit="+strconv.Itoa(pageSize)+"&offset="+strconv.Itoa(offset), nil, &data); e != nil {
			return nil, e
		}
		if data.Notepads == nil {
			return nil, cloudError("invalid_response", false)
		}
		for _, n := range data.Notepads {
			books = append(books, CloudWordbook{n.ID, n.Title})
		}
		if len(data.Notepads) < pageSize {
			return books, nil
		}
	}
	return nil, cloudError("too_many_wordbooks", false)
}
func (c *cloudClient) CreateWordbook(ctx context.Context, name string) (CloudWordbook, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return CloudWordbook{}, cloudError("invalid_name", false)
	}
	var book CloudWordbook
	if c.provider == "eudic" {
		e := c.request(ctx, "POST", "/category", map[string]any{"language": "en", "name": name}, &book)
		if e != nil {
			return book, e
		}
	} else {
		var data struct {
			Notepad memoNotepad `json:"notepad"`
		}
		e := c.request(ctx, "POST", "/notepads", map[string]any{"notepad": memoNotepad{Status: "UNPUBLISHED", Title: name, Tags: []string{}}}, &data)
		if e != nil {
			return book, e
		}
		book = CloudWordbook{data.Notepad.ID, data.Notepad.Title}
	}
	if book.ID == "" {
		return book, cloudError("invalid_response", false)
	}
	return book, nil
}

type memoNotepad struct {
	ID      string   `json:"id,omitempty"`
	Status  string   `json:"status"`
	Content string   `json:"content"`
	Title   string   `json:"title"`
	Brief   string   `json:"brief"`
	Tags    []string `json:"tags"`
}

func (c *cloudClient) AddWord(ctx context.Context, targetID, term string, addToStudy bool) error {
	release, err := c.limiter.lockOperation(ctx)
	if err != nil {
		return err
	}
	defer release()
	term = strings.TrimSpace(term)
	if targetID == "" || term == "" || strings.ContainsAny(term, "\r\n") {
		return cloudError("invalid_word", false)
	}
	if c.provider == "eudic" {
		// The documented words format preserves existing stars, unlike items (default 1).
		// A single-word retry after a partial write cannot reset review state.
		return c.request(ctx, "POST", "/words", map[string]any{"language": "en", "category_id": targetID, "words": []string{term}}, nil)
	}
	path := "/notepads/" + url.PathEscape(targetID)
	var data struct {
		Notepad json.RawMessage `json:"notepad"`
	}
	if e := c.request(ctx, "GET", path, nil, &data); e != nil {
		return e
	}
	var fields map[string]json.RawMessage
	var n memoNotepad
	if json.Unmarshal(data.Notepad, &fields) != nil || json.Unmarshal(data.Notepad, &n) != nil {
		return cloudError("invalid_response", false)
	}
	for _, key := range []string{"id", "status", "content", "title", "brief", "tags"} {
		if len(fields[key]) == 0 || string(fields[key]) == "null" {
			return cloudError("invalid_response", false)
		}
	}
	if n.ID != targetID || (n.Status != "PUBLISHED" && n.Status != "UNPUBLISHED") || n.Tags == nil {
		return cloudError("invalid_response", false)
	}
	found := false
	for _, line := range strings.Split(n.Content, "\n") {
		if strings.EqualFold(strings.TrimSpace(line), term) {
			found = true
			break
		}
	}
	if !found {
		if n.Content != "" && !strings.HasSuffix(n.Content, "\n") {
			n.Content += "\n"
		}
		n.Content += term
		n.ID = ""
		var updated struct {
			Notepad memoNotepad `json:"notepad"`
		}
		if e := c.request(ctx, "POST", path, map[string]any{"id": targetID, "notepad": n}, &updated); e != nil {
			return e
		}
		if updated.Notepad.ID != targetID {
			return cloudError("invalid_response", false)
		}
	}
	if addToStudy {
		return c.addToStudy(ctx, term)
	}
	return nil
}
func (c *cloudClient) addToStudy(ctx context.Context, term string) error {
	var vocab struct {
		Voc struct {
			ID string `json:"id"`
		} `json:"voc"`
	}
	if e := c.request(ctx, "GET", "/vocabulary?spelling="+url.QueryEscape(term), nil, &vocab); e != nil {
		return e
	}
	if vocab.Voc.ID == "" {
		return cloudError("word_not_found", false)
	}
	var result struct {
		AddedCount *int `json:"added_count"`
	}
	if e := c.request(ctx, "POST", "/study/add_words", map[string]any{"words": []map[string]string{{"id": vocab.Voc.ID}}, "advance": false}, &result); e != nil {
		return e
	}
	if result.AddedCount == nil {
		return cloudError("invalid_response", false)
	}
	if *result.AddedCount == 1 {
		return nil
	}
	if *result.AddedCount != 0 {
		return cloudError("invalid_response", false)
	}
	var records struct {
		Records []struct {
			VocID string `json:"voc_id"`
		} `json:"records"`
	}
	if e := c.request(ctx, "POST", "/study/query_study_records", map[string]any{"voc_ids": []string{vocab.Voc.ID}}, &records); e != nil {
		return e
	}
	for _, r := range records.Records {
		if r.VocID == vocab.Voc.ID {
			return nil
		}
	}
	return cloudError("study_word_not_added", false)
}
