package academic

// Zotero adapters are read-only. Credentials live for one request, and neither
// preview nor attachment download creates a document or mutates the paper graph.

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	zoteroPageSize         = 100
	zoteroMaxObjects       = 50000
	zoteroMaxMetadataBytes = 20 << 20
	zoteroMaxPDFBytes      = 128 << 20
	zoteroMaxZIPBytes      = 64 << 20
)

var zoteroItemKey = regexp.MustCompile(`^[A-Z0-9]{8}$`)
var zoteroLibraryID = regexp.MustCompile(`^[0-9]{1,20}$`)
var zoteroMD5 = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)

type zoteroWebDAVConfig struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type zoteroConfig struct {
	Source      string              `json:"source"`
	LibraryType string              `json:"library_type"`
	LibraryID   string              `json:"library_id"`
	APIKey      string              `json:"api_key,omitempty"`
	LocalURL    string              `json:"local_url,omitempty"`
	WebDAV      *zoteroWebDAVConfig `json:"webdav,omitempty"`
}

type zoteroRequest struct {
	zoteroConfig
	ItemKey string `json:"item_key,omitempty"`
}

type zoteroError struct {
	Status         int
	Code           string
	Message        string
	UpstreamStatus int
	RetryAfter     string
}

func (e *zoteroError) Error() string { return e.Message }

func zoteroFailure(status int, code, message string) *zoteroError {
	return &zoteroError{Status: status, Code: code, Message: message}
}

func replyZoteroError(w http.ResponseWriter, err error) {
	failure, ok := err.(*zoteroError)
	if !ok {
		failure = zoteroFailure(502, "ZOTERO_REQUEST_FAILED", "无法读取 Zotero，请检查连接后重试")
	}
	if failure.RetryAfter != "" {
		w.Header().Set("Retry-After", failure.RetryAfter)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(failure.Status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": failure.Code, "message": failure.Message, "upstream_status": failure.UpstreamStatus, "retry_after": failure.RetryAfter})
}

func readZoteroRequest(w http.ResponseWriter, r *http.Request) (zoteroRequest, bool) {
	var request zoteroRequest
	_, canWrite, ok := paperLibraryAccess(w, r)
	if !ok {
		return request, false
	}
	if !canWrite {
		replyZoteroError(w, zoteroFailure(403, "TARGET_DATASET_FORBIDDEN", "没有目标论文库的编辑权限"))
		return request, false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&request)
	if err == nil {
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			err = io.ErrUnexpectedEOF
		}
	}
	if err != nil {
		replyZoteroError(w, zoteroFailure(400, "INVALID_ZOTERO_REQUEST", "Zotero 导入配置无效"))
		return request, false
	}
	return request, true
}

func PreviewZoteroLibrary(w http.ResponseWriter, r *http.Request) {
	request, ok := readZoteroRequest(w, r)
	if !ok {
		return
	}
	client, err := newZoteroClient(request.zoteroConfig)
	if err != nil {
		replyZoteroError(w, err)
		return
	}
	defer client.http.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	preview, err := client.preview(ctx)
	if err != nil {
		replyZoteroError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(preview)
}

func DownloadZoteroAttachment(w http.ResponseWriter, r *http.Request) {
	request, ok := readZoteroRequest(w, r)
	if !ok {
		return
	}
	client, err := newZoteroClient(request.zoteroConfig)
	if err != nil {
		replyZoteroError(w, err)
		return
	}
	defer client.http.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	file, err := client.attachment(ctx, request.ItemKey)
	if err != nil {
		replyZoteroError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", strconv.Itoa(len(file.Data)))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(file.Data)
}

type zoteroClient struct {
	config        zoteroConfig
	base          *url.URL
	dav           *url.URL
	http          *http.Client
	version       string
	serverID      string
	metadataBytes int
}

func newZoteroClient(config zoteroConfig) (*zoteroClient, error) {
	if config.Source != "local" && config.Source != "remote" && config.Source != "webdav" {
		return nil, zoteroFailure(400, "INVALID_ZOTERO_SOURCE", "请选择本地 Zotero、远端 Zotero 或 WebDAV 来源")
	}
	if config.LibraryType == "" {
		config.LibraryType = "users"
	}
	if config.LibraryType != "users" && config.LibraryType != "groups" {
		return nil, zoteroFailure(400, "INVALID_ZOTERO_LIBRARY", "文献库类型必须为个人或群组")
	}
	if config.Source == "local" && config.LibraryID == "" {
		config.LibraryID = "0"
	}
	if !zoteroLibraryID.MatchString(config.LibraryID) || (config.Source != "local" && config.LibraryID == "0") {
		return nil, zoteroFailure(400, "INVALID_ZOTERO_LIBRARY", "请填写有效的 Zotero 数字文献库 ID")
	}
	if len(config.APIKey) > 256 || strings.ContainsAny(config.APIKey, "\r\n") {
		return nil, zoteroFailure(400, "INVALID_ZOTERO_CREDENTIALS", "Zotero API Key 格式无效")
	}
	base, _ := url.Parse("https://api.zotero.org")
	if config.Source == "local" {
		if config.LocalURL == "" {
			config.LocalURL = "http://localhost:23119/api"
		}
		var err error
		base, err = url.Parse(config.LocalURL)
		if err != nil || base.Scheme != "http" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || base.Port() != "23119" || (base.Path != "/api" && base.Path != "/api/") || (base.Hostname() != "localhost" && base.Hostname() != "127.0.0.1" && base.Hostname() != "host.docker.internal") {
			return nil, zoteroFailure(400, "INVALID_ZOTERO_LOCAL_URL", "本地连接仅支持 localhost、127.0.0.1 或 host.docker.internal 的 23119/api 地址")
		}
		config.APIKey = ""
	}
	client := &zoteroClient{config: config, base: base}
	if config.Source == "webdav" {
		if config.LibraryType == "groups" {
			return nil, zoteroFailure(400, "ZOTERO_WEBDAV_GROUP_UNSUPPORTED", "Zotero 群组附件不使用 WebDAV，请选择远端 Zotero")
		}
		if config.WebDAV == nil {
			return nil, zoteroFailure(400, "INVALID_WEBDAV_CONFIG", "请填写 WebDAV 附件地址")
		}
		dav, err := url.Parse(strings.TrimSpace(config.WebDAV.URL))
		if err != nil || !zoteroAllowedURL(dav, true) || dav.User != nil || dav.RawQuery != "" || dav.Fragment != "" {
			return nil, zoteroFailure(400, "INVALID_WEBDAV_CONFIG", "WebDAV 地址必须是有效的 HTTP 或 HTTPS 文件夹地址，且不含账号或查询参数")
		}
		if len(config.WebDAV.Username) > 512 || len(config.WebDAV.Password) > 4096 || strings.ContainsAny(config.WebDAV.Username, "\r\n:") || strings.ContainsAny(config.WebDAV.Password, "\r\n") {
			return nil, zoteroFailure(400, "INVALID_WEBDAV_CONFIG", "WebDAV 登录信息格式无效")
		}
		if path.Base(strings.TrimRight(dav.Path, "/")) != "zotero" {
			dav.Path = path.Join(dav.Path, "zotero")
		}
		dav.RawPath = ""
		client.dav = dav
	}
	client.http = &http.Client{Timeout: 45 * time.Second, Transport: &zoteroRoutingTransport{client: client, public: zoteroTransport(false), private: zoteroTransport(true)}, CheckRedirect: client.checkRedirect}
	return client, nil
}

type zoteroPreview struct {
	Items       []json.RawMessage `json:"items"`
	Collections []json.RawMessage `json:"collections"`
	SourceID    string            `json:"source_id"`
	Warnings    []string          `json:"warnings"`
}

func (c *zoteroClient) apiURL(resource string) *url.URL {
	u := *c.base
	u.Path = path.Join(c.base.Path, c.config.LibraryType, c.config.LibraryID, resource)
	return &u
}

func (c *zoteroClient) request(ctx context.Context, target *url.URL, limit int64) ([]byte, http.Header, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, nil, zoteroFailure(400, "INVALID_ZOTERO_REQUEST", "Zotero 请求地址无效")
	}
	r.Header.Set("User-Agent", "LazyMind Zotero Import")
	if target.Host == c.base.Host && target.Scheme == c.base.Scheme {
		r.Header.Set("Zotero-API-Version", "3")
		if c.config.APIKey != "" {
			r.Header.Set("Zotero-API-Key", c.config.APIKey)
		}
		if c.serverID != "" {
			r.Header.Set("Zotero-Server-ID", c.serverID)
		}
	} else if c.dav != nil && sameZoteroOrigin(target, c.dav) {
		r.SetBasicAuth(c.config.WebDAV.Username, c.config.WebDAV.Password)
	}
	resp, err := c.http.Do(r)
	if err != nil {
		var failure *zoteroError
		if errors.As(err, &failure) {
			return nil, nil, failure
		}
		if ctx.Err() != nil {
			return nil, nil, zoteroFailure(504, "ZOTERO_REQUEST_TIMEOUT", "Zotero 导入请求已取消或超时，请重试")
		}
		return nil, nil, zoteroFailure(502, "ZOTERO_CONNECTION_FAILED", "无法连接文献来源，请检查地址、服务状态及网络设置")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status := 502
		code := "ZOTERO_PROVIDER_FAILED"
		message := fmt.Sprintf("文献来源返回 HTTP %d，导入未完成", resp.StatusCode)
		switch resp.StatusCode {
		case 401, 403:
			status = 403
			code = "ZOTERO_ACCESS_DENIED"
			message = "无法访问文献来源，请检查 API Key、文献库权限或本地 Zotero 通信设置"
		case 404:
			status = 404
			code = "ZOTERO_NOT_FOUND"
			message = "文献库或附件不存在，请检查文献库 ID 和附件同步状态"
		case 412:
			status = 409
			code = "ZOTERO_SOURCE_CHANGED"
			message = "Zotero 文献库在读取期间发生变化，请重新预览"
		case 429:
			status = 429
			code = "ZOTERO_RATE_LIMITED"
			message = "文献来源请求过于频繁，请稍后重试"
		}
		failure := zoteroFailure(status, code, message)
		failure.UpstreamStatus = resp.StatusCode
		if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds >= 0 && seconds <= 86400 {
			failure.RetryAfter = strconv.Itoa(seconds)
		}
		return nil, nil, failure
	}
	if resp.ContentLength > limit {
		return nil, nil, zoteroFailure(413, "ZOTERO_RESPONSE_TOO_LARGE", "文献来源响应超过导入大小上限")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, nil, zoteroFailure(502, "ZOTERO_READ_FAILED", "读取文献来源失败，导入未完成")
	}
	if int64(len(b)) > limit {
		return nil, nil, zoteroFailure(413, "ZOTERO_RESPONSE_TOO_LARGE", "文献来源响应超过导入大小上限")
	}
	return b, resp.Header, nil
}

func (c *zoteroClient) observeVersion(headers http.Header) error {
	version := headers.Get("Last-Modified-Version")
	server := headers.Get("Zotero-Server-ID")
	if (c.version != "" && version != c.version) || (c.serverID != "" && server != c.serverID) {
		return zoteroFailure(409, "ZOTERO_SOURCE_CHANGED", "Zotero 文献库在读取期间发生变化，请重新预览")
	}
	if version != "" {
		c.version = version
	}
	if server != "" {
		c.serverID = server
	}
	return nil
}

func (c *zoteroClient) readAll(ctx context.Context, resource string) ([]json.RawMessage, error) {
	result := make([]json.RawMessage, 0)
	seen := map[string]bool{}
	expectedTotal := -1
	for start := 0; ; start += zoteroPageSize {
		u := c.apiURL(resource)
		query := u.Query()
		query.Set("format", "json")
		query.Set("limit", strconv.Itoa(zoteroPageSize))
		query.Set("start", strconv.Itoa(start))
		u.RawQuery = query.Encode()
		body, headers, err := c.request(ctx, u, 2<<20)
		if err != nil {
			return nil, err
		}
		if err = c.observeVersion(headers); err != nil {
			return nil, err
		}
		c.metadataBytes += len(body)
		if c.metadataBytes > zoteroMaxMetadataBytes {
			return nil, zoteroFailure(413, "ZOTERO_LIBRARY_TOO_LARGE", "Zotero 题录超过 20 MiB 导入上限，请分批导入")
		}
		var page []json.RawMessage
		if err = json.Unmarshal(body, &page); err != nil || page == nil || len(page) > zoteroPageSize {
			return nil, zoteroFailure(502, "INVALID_ZOTERO_RESPONSE", "Zotero 返回了无效的分页题录数据")
		}
		if rawTotal := headers.Get("Total-Results"); rawTotal != "" {
			total, parseErr := strconv.Atoi(rawTotal)
			if parseErr != nil || total < 0 {
				return nil, zoteroFailure(502, "INVALID_ZOTERO_RESPONSE", "Zotero 返回了无效的分页题录数据")
			}
			if total > zoteroMaxObjects {
				return nil, zoteroFailure(413, "ZOTERO_LIBRARY_TOO_LARGE", "Zotero 条目超过 50000 项，请分批导入")
			}
			if expectedTotal >= 0 && total != expectedTotal {
				return nil, zoteroFailure(409, "ZOTERO_SOURCE_CHANGED", "Zotero 文献库在读取期间发生变化，请重新预览")
			}
			expectedTotal = total
		}
		if expectedTotal >= 0 && (start+len(page) > expectedTotal || (len(page) < zoteroPageSize && start+len(page) < expectedTotal)) {
			return nil, zoteroFailure(502, "INVALID_ZOTERO_RESPONSE", "Zotero 返回的分页题录不完整，请重新预览")
		}
		for _, raw := range page {
			var item struct {
				Key string `json:"key"`
			}
			if json.Unmarshal(raw, &item) != nil || !zoteroItemKey.MatchString(item.Key) || seen[item.Key] {
				return nil, zoteroFailure(502, "INVALID_ZOTERO_RESPONSE", "Zotero 返回了重复或无效的条目，请重新预览")
			}
			seen[item.Key] = true
			result = append(result, raw)
		}
		if len(result) > zoteroMaxObjects {
			return nil, zoteroFailure(413, "ZOTERO_LIBRARY_TOO_LARGE", "Zotero 条目超过 50000 项，请分批导入")
		}
		if len(page) < zoteroPageSize {
			break
		}
		if expectedTotal >= 0 && start+len(page) >= expectedTotal {
			break
		}
	}
	return result, nil
}

func (c *zoteroClient) preview(ctx context.Context) (zoteroPreview, error) {
	result := zoteroPreview{Warnings: []string{}}
	var err error
	if result.Collections, err = c.readAll(ctx, "collections"); err != nil {
		return result, err
	}
	if result.Items, err = c.readAll(ctx, "items"); err != nil {
		return result, err
	}
	papers := 0
	for _, raw := range result.Items {
		var item struct {
			Data struct {
				ItemType string `json:"itemType"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &item) != nil || item.Data.ItemType == "" {
			return result, zoteroFailure(502, "INVALID_ZOTERO_RESPONSE", "Zotero 返回的题录缺少条目类型")
		}
		if item.Data.ItemType != "attachment" && item.Data.ItemType != "note" && item.Data.ItemType != "annotation" {
			papers++
		}
	}
	if papers > 10000 {
		return result, zoteroFailure(413, "ZOTERO_LIBRARY_TOO_LARGE", "单次最多导入 10000 篇文献，请分批导入")
	}
	result.SourceID = c.config.LibraryType + ":" + c.config.LibraryID
	if c.config.Source == "local" {
		identity := c.serverID
		if identity == "" {
			identity = c.base.Host
		}
		result.SourceID = "local:" + identity + ":" + c.config.LibraryType + ":" + c.config.LibraryID
		result.Warnings = append(result.Warnings, "本地 Zotero 附件需要选择本机 storage 文件夹；题录、集合和笔记可直接读取。")
	}
	return result, nil
}

type zoteroFile struct {
	Name string
	Data []byte
}

func (c *zoteroClient) attachment(ctx context.Context, key string) (zoteroFile, error) {
	var file zoteroFile
	if !zoteroItemKey.MatchString(key) {
		return file, zoteroFailure(400, "INVALID_ZOTERO_ITEM_KEY", "Zotero 附件编号无效")
	}
	body, _, err := c.request(ctx, c.apiURL("items/"+key), 2<<20)
	if err != nil {
		return file, err
	}
	var item struct {
		Key  string `json:"key"`
		Data struct {
			ItemType    string `json:"itemType"`
			ContentType string `json:"contentType"`
			Filename    string `json:"filename"`
			MD5         string `json:"md5"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &item) != nil || item.Key != key || item.Data.ItemType != "attachment" || item.Data.ContentType != "application/pdf" {
		return file, zoteroFailure(422, "ZOTERO_PDF_REQUIRED", "该 Zotero 条目不是 PDF 附件")
	}
	filename := item.Data.Filename
	if filename == "" {
		filename = key + ".pdf"
	}
	if path.Base(filename) != filename || strings.ContainsAny(filename, "\\\x00\r\n") || len(filename) > 512 {
		return file, zoteroFailure(502, "INVALID_ZOTERO_RESPONSE", "Zotero 附件文件名无效")
	}
	if c.config.Source == "webdav" {
		u := *c.dav
		u.Path = path.Join(u.Path, key+".zip")
		archive, _, err := c.request(ctx, &u, zoteroMaxZIPBytes)
		if err != nil {
			return file, err
		}
		file.Data, err = readZoteroZIP(archive, filename)
		if err != nil {
			return file, err
		}
	} else {
		file.Data, _, err = c.request(ctx, c.apiURL("items/"+key+"/file"), zoteroMaxPDFBytes)
		if err != nil {
			return file, err
		}
	}
	if len(file.Data) < 5 || !strings.HasPrefix(string(file.Data[:min(len(file.Data), 1024)]), "%PDF-") {
		return zoteroFile{}, zoteroFailure(502, "ZOTERO_INVALID_PDF", "下载的附件不是有效 PDF，可能是登录页或文件损坏")
	}
	if item.Data.MD5 != "" {
		hash := md5.Sum(file.Data)
		if !zoteroMD5.MatchString(item.Data.MD5) || !strings.EqualFold(hex.EncodeToString(hash[:]), item.Data.MD5) {
			return zoteroFile{}, zoteroFailure(409, "ZOTERO_ATTACHMENT_CHANGED", "附件与 Zotero 记录的校验值不一致，请完成同步后重试")
		}
	}
	file.Name = filename
	return file, nil
}
