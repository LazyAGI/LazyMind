package pdfir

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
	"path/filepath"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

func FromConfig(cfg map[string]any) (Client, error) {
	c := Client{Model: "vlm", BaseURL: "https://mineru.net/api/v4"}
	if cfg["ocr_type"] != "mineru" {
		return c, errors.New("Select a MinerU OCR credential in model settings")
	}
	endpoint, _ := cfg["ocr_url"].(string)
	u, e := url.Parse(strings.TrimSpace(endpoint))
	if e != nil || u.Scheme != "https" || u.Host != "mineru.net" || u.User != nil {
		return c, errors.New("Selected local/custom MinerU service does not support PDF IR; no cloud upload was performed")
	}
	auth, _ := cfg["ocr_auth"].(map[string]any)
	switch v := auth["mineru"].(type) {
	case string:
		c.APIKey = strings.TrimSpace(v)
	case []string:
		if len(v) > 0 {
			c.APIKey = strings.TrimSpace(v[0])
		}
	case []any:
		if len(v) > 0 {
			c.APIKey, _ = v[0].(string)
		}
	}
	if c.APIKey == "" {
		return c, errors.New("Selected MinerU credential is missing")
	}
	return c, nil
}
func (c Client) request(ctx context.Context, method, target string, body io.Reader, auth bool) ([]byte, error) {
	u, e := url.Parse(target)
	if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return nil, errors.New("invalid MinerU response URL")
	}
	// Presigned object URLs never receive the API credential. Restrict production
	// downloads/uploads to the official result storage; custom test clients may
	// only use their own API origin.
	if !auth {
		base, _ := url.Parse(c.BaseURL)
		testOrigin := c.HTTP != nil && base != nil && base.Host == u.Host && base.Scheme == u.Scheme && base.Hostname() == "127.0.0.1"
		official := u.Scheme == "https" && u.Port() == "" && (u.Hostname() == "mineru.net" || strings.HasSuffix(u.Hostname(), ".mineru.net") || u.Hostname() == "mineru.oss-cn-shanghai.aliyuncs.com" || u.Hostname() == "cdn-mineru.openxlab.org.cn")
		if !official && !testOrigin {
			return nil, errors.New("untrusted MinerU artifact URL")
		}
	}
	req, e := http.NewRequestWithContext(ctx, method, target, body)
	if e != nil {
		return nil, e
	}
	if f, ok := body.(*os.File); ok {
		stat, err := f.Stat()
		if err != nil {
			return nil, errors.New("PDF source unavailable")
		}
		req.ContentLength = stat.Size()
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Minute}
	}
	safe := *client
	safe.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	res, e := safe.Do(req)
	if e != nil {
		return nil, errors.New("MinerU request failed")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("MinerU HTTP %d", res.StatusCode)
	}
	limit := MaxArchiveBytes
	if auth {
		limit = 2 << 20
	}
	return bounded(res.Body, limit)
}
func (c Client) api(ctx context.Context, method, p string, body io.Reader, out any) error {
	b, e := c.request(ctx, method, c.BaseURL+p, body, true)
	if e != nil {
		return e
	}
	var envelope struct {
		Code int `json:"code"`
	}
	if json.Unmarshal(b, &envelope) != nil {
		return errors.New("invalid MinerU API response")
	}
	if envelope.Code != 0 {
		return errors.New("MinerU API rejected request")
	}
	if json.Unmarshal(b, out) != nil {
		return errors.New("invalid MinerU API response")
	}
	return nil
}
func (c Client) Parse(ctx context.Context, p string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	payload, _ := json.Marshal(map[string]any{"files": []map[string]string{{"name": filepath.Base(p)}}, "model_version": c.Model, "enable_formula": true})
	var upload struct {
		Data struct {
			BatchID string   `json:"batch_id"`
			URLs    []string `json:"file_urls"`
		} `json:"data"`
	}
	if e := c.api(ctx, "POST", "/file-urls/batch", bytes.NewReader(payload), &upload); e != nil {
		return nil, e
	}
	if upload.Data.BatchID == "" || len(upload.Data.URLs) != 1 {
		return nil, errors.New("invalid MinerU upload response")
	}
	f, e := os.Open(p)
	if e != nil {
		return nil, errors.New("PDF source unavailable")
	}
	_, e = c.request(ctx, "PUT", upload.Data.URLs[0], f, false)
	f.Close()
	if e != nil {
		return nil, e
	}
	for {
		var status struct {
			Data struct {
				Results []struct {
					State string `json:"state"`
					URL   string `json:"full_zip_url"`
				} `json:"extract_result"`
			} `json:"data"`
		}
		if e = c.api(ctx, "GET", "/extract-results/batch/"+url.PathEscape(upload.Data.BatchID), nil, &status); e != nil {
			return nil, e
		}
		if len(status.Data.Results) > 0 {
			v := status.Data.Results[0]
			if v.State == "failed" {
				return nil, errors.New("MinerU parsing failed")
			}
			if v.State == "done" {
				return c.request(ctx, "GET", v.URL, nil, false)
			}
		}
		timer := time.NewTimer(3 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
