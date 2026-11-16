package pdfir

import (
	"context"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOfficialAPISkipsTLSVerificationWithoutChangingDomain(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "mineru.net" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("unexpected API identity: %s", r.Host)
		}
		io.WriteString(w, "ok")
	}))
	defer server.Close()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	defer transport.CloseIdleConnections()
	c := Client{APIKey: "secret", HTTP: &http.Client{Transport: transport}}
	got, err := c.request(context.Background(), "GET", "https://mineru.net/api/v4/test", nil, true)
	if err != nil || string(got) != "ok" {
		t.Fatalf("API: %q %v", got, err)
	}
	if transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("shared transport mutated")
	}
	_, err = c.request(context.Background(), "GET", "https://mineru.net/artifact", nil, false)
	if err == nil {
		t.Fatal("artifact certificate was not verified")
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
