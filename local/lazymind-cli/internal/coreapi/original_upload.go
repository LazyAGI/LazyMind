package coreapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"time"
)

const MaxOriginalBytes int64 = 128 << 20

type originalUploadContextKey struct{}

// UploadOriginal accepts an already authorized open host file, not a model path.
func (c *Client) UploadOriginal(ctx context.Context, file *os.File, name string) (string, error) {
	server, err := c.ServerURL(ctx)
	if err != nil {
		return "", err
	}
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > MaxOriginalBytes {
		return "", errors.New("original must be a regular file of 1 byte to 128 MiB")
	}
	return c.uploadOriginalTo(ctx, server, file, name, info.Size())
}
func (c *Client) uploadOriginalTo(ctx context.Context, server string, file *os.File, name string, size int64) (string, error) {
	if size <= 0 || size > MaxOriginalBytes {
		return "", errors.New("original exceeds upload size limit")
	}
	var envelope bytes.Buffer
	writer := multipart.NewWriter(&envelope)
	if _, err := writer.CreateFormFile("file", name); err != nil {
		return "", err
	}
	prefix := append([]byte(nil), envelope.Bytes()...)
	if err := writer.Close(); err != nil {
		return "", err
	}
	suffix := append([]byte(nil), envelope.Bytes()[len(prefix):]...)
	body := func() (io.ReadCloser, error) {
		return io.NopCloser(io.MultiReader(bytes.NewReader(prefix), io.NewSectionReader(file, 0, size), bytes.NewReader(suffix))), nil
	}
	ctx, cancel := context.WithTimeout(context.WithValue(ctx, originalUploadContextKey{}, true), 2*time.Minute)
	defer cancel()
	initial, _ := body()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/api/core/knowledge/originals", initial)
	if err != nil {
		return "", err
	}
	request.GetBody = body
	request.ContentLength = int64(len(prefix)) + size + int64(len(suffix))
	request.Header.Set("Content-Type", writer.FormDataContentType())
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("original upload redirects are forbidden")
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		return "", err
	}
	if len(data) > 64<<10 {
		return "", errors.New("original upload response too large")
	}
	payload, err := unwrap(response.StatusCode, data)
	if err != nil {
		return "", err
	}
	var result struct {
		FileRef string `json:"file_ref"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return "", err
	}
	if result.FileRef == "" {
		return "", fmt.Errorf("original upload returned no file_ref")
	}
	return result.FileRef, nil
}
