package cloudclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"lazymind/core/cloudpackage"
)

type ResourceOperationStatus string

const (
	ResourceOperationWaitingForUpload ResourceOperationStatus = "waiting_for_upload"
	ResourceOperationVerifying        ResourceOperationStatus = "verifying"
	ResourceOperationSucceeded        ResourceOperationStatus = "succeeded"
	ResourceOperationFailed           ResourceOperationStatus = "failed"
	ResourceOperationConflicted       ResourceOperationStatus = "conflicted"
	ResourceOperationCancelled        ResourceOperationStatus = "cancelled"
	ResourceOperationExpired          ResourceOperationStatus = "expired"
)

const (
	UploadModeSinglePut = "single_put"
	UploadModeMultipart = "multipart"
)

type ResourceUpsertRequest struct {
	ResourceType      string
	ClientResourceKey string
	Manifest          cloudpackage.Manifest
	IdempotencyKey    string
	IfNoneMatch       bool
	IfMatch           string
}

type UploadAuthorization struct {
	UploadID      string
	OperationID   string
	Mode          string
	Status        string
	ExpiresAt     string
	PartSize      int64
	SignedRequest *SignedRequest
}

type ResourceOperation struct {
	OperationID string
	Status      ResourceOperationStatus
	Upload      *UploadAuthorization
	Resource    *PrivateResource
	Error       *CloudError
}

type UploadPartAuthorization struct {
	PartNumber    int
	SignedRequest SignedRequest
}

type CompletedUploadPart struct {
	PartNumber int    `json:"part_number"`
	ETag       string `json:"etag"`
}

type resourceOperationWire struct {
	OperationID string                  `json:"operation_id"`
	Status      ResourceOperationStatus `json:"status"`
	Upload      *uploadStateWire        `json:"upload,omitempty"`
	Resource    *PrivateResource        `json:"resource,omitempty"`
	Error       *CloudError             `json:"error,omitempty"`
}

type uploadStateWire struct {
	UploadID    string            `json:"upload_id"`
	OperationID string            `json:"operation_id"`
	Mode        string            `json:"mode"`
	Status      string            `json:"status"`
	URL         string            `json:"url,omitempty"`
	Method      string            `json:"method,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	ExpiresAt   string            `json:"expires_at"`
	PartSize    int64             `json:"part_size,omitempty"`
}

type uploadPartWire struct {
	PartNumber int               `json:"part_number"`
	URL        string            `json:"url"`
	Method     string            `json:"method"`
	Headers    map[string]string `json:"headers"`
	ExpiresAt  string            `json:"expires_at"`
}

func (c *Client) BeginResourceUpsert(ctx context.Context, accessToken string, input ResourceUpsertRequest) (ResourceOperation, error) {
	if err := validateBearer(accessToken); err != nil {
		return ResourceOperation{}, err
	}
	if input.ResourceType != "skill" && input.ResourceType != "workflow" || strings.TrimSpace(input.ClientResourceKey) == "" || strings.TrimSpace(input.IdempotencyKey) == "" {
		return ResourceOperation{}, errors.New("resource upsert request is incomplete")
	}
	if input.Manifest.ResourceType != input.ResourceType || input.Manifest.ClientResourceKey != input.ClientResourceKey {
		return ResourceOperation{}, errors.New("resource upsert manifest does not match the request")
	}
	if input.IfNoneMatch == (strings.TrimSpace(input.IfMatch) != "") {
		return ResourceOperation{}, errors.New("resource upsert requires exactly one condition")
	}
	if input.IfMatch != "" && !isLowerHex64(strings.TrimSpace(input.IfMatch)) {
		return ResourceOperation{}, errors.New("resource upsert If-Match is invalid")
	}
	body, err := json.Marshal(struct {
		ResourceType      string                `json:"resource_type"`
		ClientResourceKey string                `json:"client_resource_key"`
		Manifest          cloudpackage.Manifest `json:"manifest"`
	}{input.ResourceType, input.ClientResourceKey, input.Manifest})
	if err != nil {
		return ResourceOperation{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.resolve("/v1/resource-upserts"), bytes.NewReader(body))
	if err != nil {
		return ResourceOperation{}, err
	}
	setCloudHeaders(request, accessToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", strings.TrimSpace(input.IdempotencyKey))
	if input.IfNoneMatch {
		request.Header.Set("If-None-Match", "*")
	} else {
		request.Header.Set("If-Match", `"`+strings.TrimSpace(input.IfMatch)+`"`)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return ResourceOperation{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return ResourceOperation{}, decodeCloudError(response)
	}
	return decodeResourceOperation(response)
}

func (c *Client) GetResourceUpsertOperation(ctx context.Context, accessToken, operationID string) (ResourceOperation, error) {
	if err := validateBearer(accessToken); err != nil {
		return ResourceOperation{}, err
	}
	if !isSafeCloudID(operationID) {
		return ResourceOperation{}, errors.New("invalid operation_id")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.resolve("/v1/resource-upserts/"+operationID), nil)
	if err != nil {
		return ResourceOperation{}, err
	}
	setCloudHeaders(request, accessToken)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return ResourceOperation{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ResourceOperation{}, decodeCloudError(response)
	}
	return decodeResourceOperation(response)
}

func (c *Client) PresignUploadParts(ctx context.Context, accessToken, uploadID string, partNumbers []int) ([]UploadPartAuthorization, error) {
	if err := validateBearer(accessToken); err != nil {
		return nil, err
	}
	if !isSafeCloudID(uploadID) || len(partNumbers) == 0 || len(partNumbers) > 50 {
		return nil, errors.New("invalid upload part request")
	}
	numbers := append([]int(nil), partNumbers...)
	sort.Ints(numbers)
	for index, number := range numbers {
		if number < 1 || number > 10000 || index > 0 && numbers[index-1] == number {
			return nil, errors.New("invalid upload part numbers")
		}
	}
	body, _ := json.Marshal(map[string]any{"part_numbers": numbers})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.resolve("/v1/transfers/uploads/"+uploadID+"/parts:presign"), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	setCloudHeaders(request, accessToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, decodeCloudError(response)
	}
	var wire struct {
		Items []uploadPartWire `json:"items"`
	}
	if err := decodeStrictJSON(response, &wire); err != nil || len(wire.Items) != len(numbers) {
		return nil, errors.New("LazyMind Cloud returned invalid upload part authorizations")
	}
	items := make([]UploadPartAuthorization, len(wire.Items))
	for index, item := range wire.Items {
		signed := SignedRequest{URL: item.URL, Method: item.Method, Headers: item.Headers, ExpiresAt: item.ExpiresAt}
		if item.PartNumber != numbers[index] || validateSignedUploadRequest(signed) != nil {
			return nil, errors.New("LazyMind Cloud returned invalid upload part authorization")
		}
		items[index] = UploadPartAuthorization{PartNumber: item.PartNumber, SignedRequest: signed}
	}
	return items, nil
}

func (c *Client) CompleteResourceUpload(ctx context.Context, accessToken, uploadID string, parts []CompletedUploadPart) error {
	if err := validateBearer(accessToken); err != nil {
		return err
	}
	if !isSafeCloudID(uploadID) {
		return errors.New("invalid upload_id")
	}
	body, err := json.Marshal(map[string]any{"parts": parts})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.resolve("/v1/transfers/uploads/"+uploadID+":complete"), bytes.NewReader(body))
	if err != nil {
		return err
	}
	setCloudHeaders(request, accessToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return decodeCloudError(response)
	}
	var wire uploadStateWire
	if err := decodeStrictJSON(response, &wire); err != nil {
		return fmt.Errorf("decode LazyMind Cloud upload completion: %w", err)
	}
	if _, err := uploadAuthorizationFromWire(wire); err != nil {
		return err
	}
	return nil
}

func (c *Client) CancelResourceUpload(ctx context.Context, accessToken, uploadID string) error {
	if err := validateBearer(accessToken); err != nil {
		return err
	}
	if !isSafeCloudID(uploadID) {
		return errors.New("invalid upload_id")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.resolve("/v1/transfers/uploads/"+uploadID), nil)
	if err != nil {
		return err
	}
	setCloudHeaders(request, accessToken)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return decodeCloudError(response)
	}
	return nil
}

func (c *Client) PutSignedFile(ctx context.Context, signed SignedRequest, filePath string, offset, length int64) (string, error) {
	if c == nil || c.httpClient == nil || strings.TrimSpace(filePath) == "" || offset < 0 || length <= 0 {
		return "", errors.New("signed file upload is not configured")
	}
	if err := validateSignedUploadRequest(signed); err != nil {
		return "", err
	}
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || offset > info.Size() || length > info.Size()-offset {
		return "", errors.New("signed file upload range is invalid")
	}
	section := io.NewSectionReader(file, offset, length)
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, signed.URL, section)
	if err != nil {
		return "", err
	}
	request.ContentLength = length
	for key, value := range signed.Headers {
		request.Header.Set(key, value)
	}
	objectClient := *c.httpClient
	objectClient.Jar = nil
	objectClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := objectClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("signed object upload failed: status=%d", response.StatusCode)
	}
	return strings.TrimSpace(response.Header.Get("ETag")), nil
}

func decodeResourceOperation(response *http.Response) (ResourceOperation, error) {
	var wire resourceOperationWire
	if err := decodeStrictJSON(response, &wire); err != nil {
		return ResourceOperation{}, fmt.Errorf("decode LazyMind Cloud resource operation: %w", err)
	}
	if !isSafeCloudID(wire.OperationID) || !validResourceOperationStatus(wire.Status) {
		return ResourceOperation{}, errors.New("LazyMind Cloud returned an invalid resource operation")
	}
	operation := ResourceOperation{OperationID: wire.OperationID, Status: wire.Status, Resource: wire.Resource, Error: wire.Error}
	if operation.Resource != nil {
		if err := validatePrivateResource(*operation.Resource); err != nil {
			return ResourceOperation{}, err
		}
	}
	if wire.Upload != nil {
		if wire.Upload.OperationID == "" {
			wire.Upload.OperationID = wire.OperationID
		}
		if wire.Upload.Status == "" && wire.Status == ResourceOperationWaitingForUpload {
			wire.Upload.Status = "uploading"
		}
		upload, err := uploadAuthorizationFromWire(*wire.Upload)
		if err != nil {
			return ResourceOperation{}, err
		}
		operation.Upload = &upload
	}
	return operation, nil
}

func uploadAuthorizationFromWire(wire uploadStateWire) (UploadAuthorization, error) {
	if !isSafeCloudID(wire.UploadID) || !isSafeCloudID(wire.OperationID) || wire.Mode != UploadModeSinglePut && wire.Mode != UploadModeMultipart {
		return UploadAuthorization{}, errors.New("LazyMind Cloud returned invalid upload state")
	}
	if _, err := time.Parse(time.RFC3339, wire.ExpiresAt); err != nil {
		return UploadAuthorization{}, errors.New("LazyMind Cloud returned invalid upload expiry")
	}
	upload := UploadAuthorization{
		UploadID: wire.UploadID, OperationID: wire.OperationID, Mode: wire.Mode, Status: wire.Status,
		ExpiresAt: wire.ExpiresAt, PartSize: wire.PartSize,
	}
	if wire.URL != "" || wire.Method != "" || len(wire.Headers) != 0 {
		signed := SignedRequest{URL: wire.URL, Method: wire.Method, Headers: wire.Headers, ExpiresAt: wire.ExpiresAt}
		if err := validateSignedUploadRequest(signed); err != nil {
			return UploadAuthorization{}, err
		}
		upload.SignedRequest = &signed
	}
	if wire.Mode == UploadModeSinglePut && upload.Status == "uploading" && upload.SignedRequest == nil {
		return UploadAuthorization{}, errors.New("single PUT upload omitted signed request")
	}
	if wire.Mode == UploadModeMultipart && wire.Status == "uploading" && wire.PartSize <= 0 {
		return UploadAuthorization{}, errors.New("multipart upload omitted part_size")
	}
	return upload, nil
}

func validateSignedUploadRequest(signed SignedRequest) error {
	parsed, err := url.Parse(strings.TrimSpace(signed.URL))
	if err != nil || parsed.Host == "" || signed.Method != http.MethodPut || parsed.User != nil {
		return errors.New("invalid signed upload request")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname())) {
		return errors.New("signed upload request must use HTTPS")
	}
	expiresAt, err := time.Parse(time.RFC3339, signed.ExpiresAt)
	if err != nil || !expiresAt.After(time.Now()) {
		return errors.New("signed upload request is expired")
	}
	for key := range signed.Headers {
		if strings.EqualFold(key, "Authorization") || strings.EqualFold(key, "Cookie") {
			return errors.New("signed upload request contains Cloud credentials")
		}
	}
	return nil
}

func validResourceOperationStatus(status ResourceOperationStatus) bool {
	switch status {
	case ResourceOperationWaitingForUpload, ResourceOperationVerifying, ResourceOperationSucceeded,
		ResourceOperationFailed, ResourceOperationConflicted, ResourceOperationCancelled, ResourceOperationExpired:
		return true
	default:
		return false
	}
}
