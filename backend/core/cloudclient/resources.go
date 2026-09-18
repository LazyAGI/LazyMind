package cloudclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type ResourceQuery struct {
	ResourceType string
	Cursor       string
	PageSize     int
}

type PrivateResource struct {
	ResourceID        string `json:"resource_id"`
	ResourceType      string `json:"resource_type"`
	ClientResourceKey string `json:"client_resource_key"`
	ResourceName      string `json:"resource_name"`
	ContentHash       string `json:"content_hash"`
	ContentSize       int64  `json:"content_size"`
	FormatSchema      string `json:"format_schema"`
	UpdatedAt         string `json:"updated_at"`
}

type ResourcePage struct {
	Items      []PrivateResource `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

type SignedRequest struct {
	URL       string            `json:"url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt string            `json:"expires_at"`
}

type DownloadStatus string

const (
	DownloadReady     DownloadStatus = "ready"
	DownloadPreparing DownloadStatus = "preparing"
)

type DownloadAuthorization struct {
	Status            DownloadStatus
	DownloadID        string
	RetryAfterSeconds int
	Request           *SignedRequest
}

func (c *Client) ListResources(ctx context.Context, accessToken string, query ResourceQuery) (ResourcePage, error) {
	if err := validateBearer(accessToken); err != nil {
		return ResourcePage{}, err
	}
	resourceType := strings.TrimSpace(query.ResourceType)
	if resourceType != "skill" && resourceType != "workflow" {
		return ResourcePage{}, errors.New("resource_type must be skill or workflow")
	}
	if len(query.Cursor) > 2048 || query.PageSize < 0 || query.PageSize > 100 {
		return ResourcePage{}, errors.New("invalid resource page query")
	}
	endpoint, err := url.Parse(c.resolve("/v1/resources"))
	if err != nil {
		return ResourcePage{}, err
	}
	values := endpoint.Query()
	values.Set("resource_type", resourceType)
	if cursor := strings.TrimSpace(query.Cursor); cursor != "" {
		values.Set("cursor", cursor)
	}
	if query.PageSize > 0 {
		values.Set("page_size", strconv.Itoa(query.PageSize))
	}
	endpoint.RawQuery = values.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return ResourcePage{}, err
	}
	setCloudHeaders(request, accessToken)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return ResourcePage{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ResourcePage{}, decodeCloudError(response)
	}
	var page ResourcePage
	if err := decodeStrictJSON(response, &page); err != nil {
		return ResourcePage{}, fmt.Errorf("decode LazyMind Cloud resource page: %w", err)
	}
	if page.Items == nil {
		page.Items = []PrivateResource{}
	}
	if len(page.Items) > 100 {
		return ResourcePage{}, errors.New("LazyMind Cloud resource page exceeds the item limit")
	}
	for _, resource := range page.Items {
		if err := validatePrivateResource(resource); err != nil {
			return ResourcePage{}, err
		}
	}
	return page, nil
}

func (c *Client) GetResource(ctx context.Context, accessToken, resourceID string) (PrivateResource, string, error) {
	if err := validateBearer(accessToken); err != nil {
		return PrivateResource{}, "", err
	}
	if !isSafeCloudID(resourceID) {
		return PrivateResource{}, "", errors.New("invalid resource_id")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.resolve("/v1/resources/"+resourceID), nil)
	if err != nil {
		return PrivateResource{}, "", err
	}
	setCloudHeaders(request, accessToken)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return PrivateResource{}, "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return PrivateResource{}, "", decodeCloudError(response)
	}
	var resource PrivateResource
	if err := decodeStrictJSON(response, &resource); err != nil {
		return PrivateResource{}, "", fmt.Errorf("decode LazyMind Cloud resource: %w", err)
	}
	if err := validatePrivateResource(resource); err != nil {
		return PrivateResource{}, "", err
	}
	etag := strings.TrimSpace(response.Header.Get("ETag"))
	if etag == "" {
		return PrivateResource{}, "", errors.New("LazyMind Cloud resource response omitted ETag")
	}
	return resource, etag, nil
}

func (c *Client) AuthorizeResourceDownload(ctx context.Context, accessToken, resourceID, etag string) (DownloadAuthorization, error) {
	if err := validateBearer(accessToken); err != nil {
		return DownloadAuthorization{}, err
	}
	if !isSafeCloudID(resourceID) || strings.TrimSpace(etag) == "" {
		return DownloadAuthorization{}, errors.New("resource_id and ETag are required")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.resolve("/v1/resources/"+resourceID+"/download-authorizations"), bytes.NewBufferString("{}"))
	if err != nil {
		return DownloadAuthorization{}, err
	}
	setCloudHeaders(request, accessToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", strings.TrimSpace(etag))
	response, err := c.httpClient.Do(request)
	if err != nil {
		return DownloadAuthorization{}, err
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
		var signed SignedRequest
		if err := decodeStrictJSON(response, &signed); err != nil {
			return DownloadAuthorization{}, fmt.Errorf("decode LazyMind Cloud download authorization: %w", err)
		}
		parsed, err := url.Parse(strings.TrimSpace(signed.URL))
		expiresAt, expiresErr := time.Parse(time.RFC3339, signed.ExpiresAt)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || signed.Method != http.MethodGet || expiresErr != nil || !expiresAt.After(time.Now()) {
			return DownloadAuthorization{}, errors.New("LazyMind Cloud returned an invalid signed download request")
		}
		return DownloadAuthorization{Status: DownloadReady, Request: &signed}, nil
	case http.StatusAccepted:
		var preparing struct {
			Status     string `json:"status"`
			DownloadID string `json:"download_id"`
		}
		if err := decodeStrictJSON(response, &preparing); err != nil || preparing.Status != string(DownloadPreparing) || strings.TrimSpace(preparing.DownloadID) == "" {
			return DownloadAuthorization{}, errors.New("LazyMind Cloud returned an invalid preparing response")
		}
		retryAfter, _ := strconv.Atoi(strings.TrimSpace(response.Header.Get("Retry-After")))
		if retryAfter < 0 {
			retryAfter = 0
		}
		return DownloadAuthorization{Status: DownloadPreparing, DownloadID: preparing.DownloadID, RetryAfterSeconds: retryAfter}, nil
	default:
		return DownloadAuthorization{}, decodeCloudError(response)
	}
}

func validatePrivateResource(resource PrivateResource) error {
	if !isSafeCloudID(resource.ResourceID) || (resource.ResourceType != "skill" && resource.ResourceType != "workflow") ||
		strings.TrimSpace(resource.ClientResourceKey) == "" || strings.TrimSpace(resource.ResourceName) == "" ||
		resource.ContentSize < 0 || resource.FormatSchema != "lazymind.resource-manifest/v2" || !isLowerHex64(resource.ContentHash) {
		return errors.New("LazyMind Cloud returned an invalid private resource")
	}
	if _, err := time.Parse(time.RFC3339, resource.UpdatedAt); err != nil {
		return errors.New("LazyMind Cloud returned an invalid resource timestamp")
	}
	return nil
}

func isLowerHex64(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') {
			continue
		}
		return false
	}
	return true
}

func isSafeCloudID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}
