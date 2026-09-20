package cloudclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

const ResourceTreeLimit = 8 << 20
const ResourceContentLimit = 16 << 20
const ResourcePreviewLimit = 2 << 20

func ValidResourceID(value string) bool {
	return value == strings.TrimSpace(value) && isSafeCloudID(value)
}

type ResourceFile struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	Executable bool   `json:"executable,omitempty"`
}

type ResourceTree struct {
	ResourceID   string         `json:"resource_id"`
	ResourceType string         `json:"resource_type"`
	ContentHash  string         `json:"content_hash"`
	Entrypoint   string         `json:"entrypoint"`
	Files        []ResourceFile `json:"files"`
}

type ResourceFileContent struct {
	Path          string `json:"path"`
	ContentHash   string `json:"content_hash"`
	SHA256        string `json:"sha256"`
	Size          int64  `json:"size"`
	MIME          string `json:"mime"`
	Binary        bool   `json:"binary"`
	Content       string `json:"content"`
	PreviewStatus string `json:"preview_status"`
}

func ValidResourcePath(value string) bool {
	if value == "" || len(value) > 1024 || !utf8.ValidString(value) || strings.ContainsAny(value, "\\:") || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || path.Clean(value) != value {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." || segment == "" {
			return false
		}
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}

func ResourceHashFromETag(value string) (string, bool) {
	if len(value) != 66 || value[0] != '"' || value[65] != '"' {
		return "", false
	}
	hash := value[1:65]
	return hash, isLowerHex64(hash)
}

func (c *Client) GetResourceTree(ctx context.Context, token, id string) (ResourceTree, string, error) {
	if !isSafeCloudID(id) {
		return ResourceTree{}, "", errors.New("invalid cloud resource id")
	}
	var result ResourceTree
	etag, err := c.readJSON(ctx, token, "/v1/resources/"+id+"/tree", nil, "", ResourceTreeLimit, &result)
	if err != nil {
		return result, "", err
	}
	if result.ResourceID != id || (result.ResourceType != "skill" && result.ResourceType != "workflow") || !isLowerHex64(result.ContentHash) || !ValidResourcePath(result.Entrypoint) || len(result.Files) == 0 || len(result.Files) > 10000 {
		return ResourceTree{}, "", errors.New("invalid cloud resource tree")
	}
	seen, entryFound := map[string]bool{}, false
	for _, file := range result.Files {
		if !ValidResourcePath(file.Path) || seen[file.Path] || !isLowerHex64(file.SHA256) || file.Size < 0 || file.Size > 100<<20 {
			return ResourceTree{}, "", errors.New("invalid cloud resource file")
		}
		seen[file.Path] = true
		entryFound = entryFound || file.Path == result.Entrypoint
	}
	if !entryFound || etag != `"`+result.ContentHash+`"` {
		return ResourceTree{}, "", errors.New("invalid cloud resource tree version")
	}
	return result, etag, nil
}

func (c *Client) ReadResourceContent(ctx context.Context, token, id, filePath, etag string) (ResourceFileContent, error) {
	hash, ok := ResourceHashFromETag(etag)
	if !ok || !isSafeCloudID(id) || !ValidResourcePath(filePath) {
		return ResourceFileContent{}, errors.New("invalid cloud content request")
	}
	var result ResourceFileContent
	_, err := c.readJSON(ctx, token, "/v1/resources/"+id+"/content", url.Values{"path": {filePath}}, etag, ResourceContentLimit, &result)
	if err != nil {
		return result, err
	}
	if result.Path != filePath || result.ContentHash != hash || !isLowerHex64(result.SHA256) || result.Size < 0 || result.Size > 100<<20 || len(result.MIME) > 256 {
		return ResourceFileContent{}, errors.New("invalid cloud file content")
	}
	switch result.PreviewStatus {
	case "ready":
		sum := sha256.Sum256([]byte(result.Content))
		if result.Binary || result.Size > ResourcePreviewLimit || int64(len(result.Content)) != result.Size || !utf8.ValidString(result.Content) || strings.ContainsRune(result.Content, 0) || hex.EncodeToString(sum[:]) != result.SHA256 {
			return ResourceFileContent{}, errors.New("invalid cloud text content")
		}
	case "binary":
		if !result.Binary || result.Content != "" {
			return ResourceFileContent{}, errors.New("invalid cloud binary preview")
		}
	case "too_large":
		if result.Size <= ResourcePreviewLimit || result.Content != "" {
			return ResourceFileContent{}, errors.New("invalid cloud large file preview")
		}
	default:
		return ResourceFileContent{}, errors.New("invalid cloud preview status")
	}
	return result, nil
}

// readJSON is used only for the bounded Desktop read contracts. Credentials
// remain in Core and are never returned with the decoded payload.
func (c *Client) readJSON(ctx context.Context, token, endpoint string, query url.Values, etag string, limit int64, target any) (string, error) {
	if err := validateBearer(token); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	uri, err := url.Parse(c.resolve(endpoint))
	if err != nil {
		return "", err
	}
	uri.RawQuery = query.Encode()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, uri.String(), nil)
	if err != nil {
		return "", err
	}
	setCloudHeaders(r, token)
	if etag != "" {
		r.Header.Set("If-Match", etag)
	}
	response, err := c.httpClient.Do(r)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var cloudErr CloudError
		_ = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&cloudErr)
		cloudErr.HTTPStatus = response.StatusCode
		if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds >= 0 && seconds <= 86400 {
			cloudErr.RetryAfterSeconds = &seconds
		}
		return "", &cloudErr
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return "", err
	}
	if int64(len(body)) > limit || !utf8.Valid(body) {
		return "", errors.New("cloud read response exceeds its contract")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return "", fmt.Errorf("decode cloud read response: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return "", errors.New("cloud read response contains extra data")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return response.Header.Get("ETag"), nil
}
