package arxivsource

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	OfficialAPIURL   = "https://export.arxiv.org/api/query"
	defaultInterval  = 3 * time.Second
	defaultTimeout   = 30 * time.Second
	maxResponseBytes = 2 * 1024 * 1024
)

// Metadata describes a verified revision. Version is always nonempty on success.
type Metadata struct {
	Title    string   `json:"title"`
	Authors  []string `json:"authors"`
	Year     int      `json:"year"`
	Abstract string   `json:"abstract"`
	DOI      string   `json:"doi,omitempty"`
	Venue    string   `json:"venue"`
	BaseID   string   `json:"base_id"`
	Version  string   `json:"version"`
	AbsURL   string   `json:"abs_url"`
	PDFURL   string   `json:"pdf_url"`
}

// LookupError lets callers distinguish invalid input, upstream HTTP/network
// failures, malformed responses, absent results, and identifier mismatches.
type LookupError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	StatusCode int    `json:"status_code,omitempty"`
	Err        error  `json:"-"`
}

func (e *LookupError) Error() string { return e.Message }
func (e *LookupError) Unwrap() error { return e.Err }

// Options provides dependency injection for trusted callers and tests. User
// input must never populate APIBaseURL. NewClient fixes the production endpoint.
// A zero MinInterval/Timeout uses the production default.
type Options struct {
	HTTPClient  *http.Client
	APIBaseURL  string
	MinInterval time.Duration
	Timeout     time.Duration
}

type Client struct {
	httpClient *http.Client
	apiBaseURL string
	timeout    time.Duration
	gate       *requestGate
}

// A process-wide gate also paces separately constructed production clients.
// arXiv requires one connection and at least three seconds between API calls:
// https://info.arxiv.org/help/api/tou.html
var officialGate = newRequestGate(defaultInterval)

func NewClient() *Client { return NewClientWithOptions(Options{}) }

func NewClientWithOptions(options Options) *Client {
	apiURL := options.APIBaseURL
	if apiURL == "" {
		apiURL = OfficialAPIURL
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	interval := options.MinInterval
	if interval <= 0 {
		interval = defaultInterval
	}
	gate := newRequestGate(interval)
	if apiURL == OfficialAPIURL {
		gate = officialGate
	}
	httpClient := *http.DefaultClient
	if options.HTTPClient != nil {
		httpClient = *options.HTTPClient
	}
	// Do not follow a provider redirect to an arbitrary hostname. The fixed
	// official HTTPS endpoint should return Atom directly.
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{httpClient: &httpClient, apiBaseURL: apiURL, timeout: timeout, gate: gate}
}

// Lookup queries id_list, which preserves explicit arXiv versions. An
// unversioned identifier is resolved to the version returned by arXiv; both
// canonical links are then pinned to that revision. No placeholder is returned.
// https://info.arxiv.org/help/api/user-manual.html#511-a-note-on-article-versions
func (c *Client) Lookup(ctx context.Context, input string) (Metadata, error) {
	id, err := Parse(input)
	if err != nil {
		return Metadata{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	endpoint, err := url.Parse(c.apiBaseURL)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return Metadata{}, &LookupError{Code: "network_error", Message: "arXiv API 地址配置无效", Err: err}
	}
	query := endpoint.Query()
	query.Set("id_list", id.ID())
	query.Set("max_results", "1")
	query.Del("search_query")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Metadata{}, networkError(err)
	}
	request.Header.Set("Accept", "application/atom+xml, application/xml")
	request.Header.Set("User-Agent", "LazyMind/1.0 (arXiv metadata importer)")
	if err = c.gate.acquire(ctx); err != nil {
		return Metadata{}, networkError(err)
	}
	defer c.gate.release()
	response, err := c.httpClient.Do(request)
	if err != nil {
		return Metadata{}, networkError(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Metadata{}, &LookupError{Code: "http_error", Message: fmt.Sprintf("arXiv 元数据服务返回 HTTP %d，查询失败", response.StatusCode), StatusCode: response.StatusCode}
	}
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "html") {
		return Metadata{}, invalidResponse("arXiv 查询返回了 HTML 网页，而非论文元数据；请检查服务或代理配置", nil)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return Metadata{}, networkError(err)
	}
	if len(body) > maxResponseBytes {
		return Metadata{}, invalidResponse("arXiv 元数据响应过大", nil)
	}
	return decodeMetadata(body, id)
}

type atomFeed struct {
	XMLName xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	XMLName   xml.Name `xml:"http://www.w3.org/2005/Atom entry"`
	ID        string   `xml:"http://www.w3.org/2005/Atom id"`
	Title     string   `xml:"http://www.w3.org/2005/Atom title"`
	Summary   string   `xml:"http://www.w3.org/2005/Atom summary"`
	Published string   `xml:"http://www.w3.org/2005/Atom published"`
	Authors   []struct {
		Name string `xml:"http://www.w3.org/2005/Atom name"`
	} `xml:"http://www.w3.org/2005/Atom author"`
	DOI string `xml:"http://arxiv.org/schemas/atom doi"`
}

func decodeMetadata(body []byte, requested Identifier) (Metadata, error) {
	var feed atomFeed
	decoder := xml.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&feed); err != nil {
		return Metadata{}, invalidResponse("arXiv 返回的内容不是有效 Atom 文献数据", err)
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Metadata{}, invalidResponse("arXiv Atom 响应不完整", err)
		}
		if spaces, ok := token.(xml.CharData); ok && strings.TrimSpace(string(spaces)) == "" {
			continue
		}
		if _, ok := token.(xml.Comment); ok {
			continue
		}
		return Metadata{}, invalidResponse("arXiv Atom 响应包含多余内容", nil)
	}
	if len(feed.Entries) == 0 {
		return Metadata{}, &LookupError{Code: "not_found", Message: "arXiv 未返回这篇论文或指定版本，请核对编号"}
	}
	if len(feed.Entries) != 1 {
		return Metadata{}, invalidResponse("arXiv 返回了多个结果，无法唯一确认论文", nil)
	}
	entry := feed.Entries[0]
	if strings.EqualFold(cleanText(entry.Title), "error") || strings.Contains(entry.ID, "/api/errors#") {
		return Metadata{}, invalidResponse("arXiv 元数据服务报错："+cleanText(entry.Summary), nil)
	}
	actual, err := Parse(strings.TrimSpace(entry.ID))
	if err != nil {
		return Metadata{}, invalidResponse("arXiv 响应中的论文标识符无效", err)
	}
	if actual.Version == "" {
		return Metadata{}, invalidResponse("arXiv 响应缺少实际版本号，无法锁定论文版本", nil)
	}
	if actual.BaseID != requested.BaseID || (requested.Version != "" && actual.Version != requested.Version) {
		return Metadata{}, &LookupError{Code: "identifier_mismatch", Message: fmt.Sprintf("arXiv 返回的论文或版本不匹配：请求 %s，返回 %s", requested.ID(), actual.ID())}
	}
	title := cleanText(entry.Title)
	if title == "" {
		return Metadata{}, invalidResponse("arXiv 响应缺少论文标题", nil)
	}
	if _, err := Parse(title); err == nil {
		return Metadata{}, invalidResponse("arXiv 响应使用论文编号代替真实标题", nil)
	}
	var authors []string
	for _, author := range entry.Authors {
		name := cleanText(author.Name)
		if name == "" {
			return Metadata{}, invalidResponse("arXiv 响应中的作者信息不完整", nil)
		}
		authors = append(authors, name)
	}
	if len(authors) == 0 {
		return Metadata{}, invalidResponse("arXiv 响应缺少作者信息", nil)
	}
	published, err := time.Parse(time.RFC3339, strings.TrimSpace(entry.Published))
	if err != nil {
		return Metadata{}, invalidResponse("arXiv 响应缺少有效发表日期", err)
	}
	abstract := cleanText(entry.Summary)
	if abstract == "" {
		return Metadata{}, invalidResponse("arXiv 响应缺少论文摘要", nil)
	}
	return Metadata{Title: title, Authors: authors, Year: published.Year(), Abstract: abstract, DOI: cleanText(entry.DOI), Venue: "arXiv", BaseID: actual.BaseID, Version: actual.Version, AbsURL: actual.AbsURL(), PDFURL: actual.PDFURL()}, nil
}

func cleanText(value string) string { return strings.Join(strings.Fields(value), " ") }
func invalidResponse(message string, err error) error {
	return &LookupError{Code: "invalid_response", Message: message, Err: err}
}
func networkError(err error) error {
	return &LookupError{Code: "network_error", Message: "arXiv 元数据查询失败：" + err.Error(), Err: err}
}

type requestGate struct {
	semaphore chan struct{}
	interval  time.Duration
	next      time.Time
}

func newRequestGate(interval time.Duration) *requestGate {
	return &requestGate{semaphore: make(chan struct{}, 1), interval: interval}
}

// Hold the semaphore until the response body has been consumed, so requests
// cannot overlap even when an upstream request takes longer than the interval.
func (g *requestGate) acquire(ctx context.Context) error {
	select {
	case g.semaphore <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		g.release()
		return err
	}
	if wait := time.Until(g.next); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			g.release()
			return ctx.Err()
		}
	}
	g.next = time.Now().Add(g.interval)
	return nil
}

func (g *requestGate) release() { <-g.semaphore }
