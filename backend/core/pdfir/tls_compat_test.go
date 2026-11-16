package pdfir

import (
	"context"
	"crypto/x509"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOfficialAPICertificateAliasRetryPreservesBody(t *testing.T) {
	calls := 0
	c := Client{BaseURL: "https://mineru.net/api/v4", APIKey: "secret", HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		b, _ := io.ReadAll(r.Body)
		if string(b) != "payload" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatal("lost API request")
		}
		if calls == 1 {
			return nil, x509.HostnameError{Host: "mineru.net", Certificate: &x509.Certificate{DNSNames: []string{"*.mineru.org.cn", "mineru.org.cn"}}}
		}
		if r.URL.String() != "https://mineru.org.cn/api/v4/file-urls/batch" {
			t.Fatal(r.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})}}
	got, err := c.request(context.Background(), "POST", c.BaseURL+"/file-urls/batch", strings.NewReader("payload"), true)
	if err != nil || string(got) != "ok" || calls != 2 {
		t.Fatalf("alias retry: %q %v calls=%d", got, err, calls)
	}
}

func TestCertificateRetryNeverChangesArtifactOrCustomOrigin(t *testing.T) {
	for _, tt := range []struct {
		url  string
		auth bool
		host string
	}{
		{"https://mineru.oss-cn-shanghai.aliyuncs.com/upload?signature=secret", false, "mineru.oss-cn-shanghai.aliyuncs.com"},
		{"https://custom.example/api/v4", true, "custom.example"},
		{"https://mineru.net/api/v4", true, "wrong.example"},
	} {
		t.Run(tt.host, func(t *testing.T) {
			calls := 0
			c := Client{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				return nil, x509.HostnameError{Host: tt.host, Certificate: &x509.Certificate{DNSNames: []string{"mineru.org.cn"}}}
			})}}
			_, err := c.request(context.Background(), "GET", tt.url, nil, tt.auth)
			if err == nil || calls != 1 || strings.Contains(err.Error(), "signature") {
				t.Fatalf("unsafe retry: calls=%d err=%v", calls, err)
			}
		})
	}
}
