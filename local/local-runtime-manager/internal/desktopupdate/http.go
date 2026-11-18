package desktopupdate

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

const maxHTTPAttempts = 3
const maxRetryDelay = 30 * time.Second

func updateHTTPClient(c Config, provided *http.Client) *http.Client {
	client := http.Client{}
	if provided != nil {
		client = *provided
	}
	// These are public update sources; do not attach browser/session cookies.
	client.Jar = nil
	if client.Transport == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.ResponseHeaderTimeout = 30 * time.Second
		client.Transport = transport
	}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return failure("redirect_limit", nil)
		}
		_, err := ResolveURL(c, request.URL.String())
		return err
	}
	return &client
}

func retryAfter(value string, attempt int) (time.Duration, error) {
	delay := time.Duration(1<<attempt) * 250 * time.Millisecond
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		if seconds > int64(maxRetryDelay/time.Second) {
			return 0, failure("rate_limited", nil)
		}
		return time.Duration(seconds) * time.Second, nil
	}
	if deadline, err := http.ParseTime(value); err == nil {
		delay = time.Until(deadline)
		if delay < 0 {
			delay = 0
		}
		if delay > maxRetryDelay {
			return 0, failure("rate_limited", nil)
		}
	}
	return delay, nil
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryableNetworkError(err error) bool {
	var classified *Failure
	if errors.As(err, &classified) {
		return classified.Code == "network_timeout" || classified.Code == "network_unavailable"
	}
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalidCertificate x509.CertificateInvalidError
	if errors.As(err, &unknownAuthority) || errors.As(err, &hostname) || errors.As(err, &invalidCertificate) {
		return false
	}
	var network net.Error
	return errors.As(err, &network) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

type idleResponseBody struct {
	io.ReadCloser
	timer *time.Timer
}

func (b *idleResponseBody) Read(buffer []byte) (int, error) {
	b.timer.Reset(time.Minute)
	n, err := b.ReadCloser.Read(buffer)
	if err != nil {
		b.timer.Stop()
	}
	return n, err
}

// withHTTP bounds retries for both policies and artifacts. Each body callback
// owns its temporary output, so a retry never appends to an unverified prefix.
func withHTTP(ctx context.Context, c Config, provided *http.Client, address string, consume func(context.Context, *http.Response) error) error {
	resolved, err := ResolveURL(c, address)
	if err != nil {
		return err
	}
	client := updateHTTPClient(c, provided)
	for attempt := 0; attempt < maxHTTPAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		attemptCtx, cancel := context.WithCancel(ctx)
		request, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, resolved, nil)
		if err != nil {
			cancel()
			return failure("source_not_allowed", err)
		}
		request.Header.Set("Accept-Encoding", "identity")
		request.Header.Set("Cache-Control", "no-cache")
		response, requestErr := client.Do(request)
		delay := time.Duration(1<<attempt) * 250 * time.Millisecond
		retry := false
		if requestErr != nil {
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			err = requestErr
			retry = retryableNetworkError(err)
			if retry {
				err = failure("source_unavailable", err)
			}
		} else {
			switch response.StatusCode {
			case http.StatusOK:
				var idleExpired atomic.Bool
				timer := time.AfterFunc(time.Minute, func() { idleExpired.Store(true); cancel() })
				response.Body = &idleResponseBody{ReadCloser: response.Body, timer: timer}
				err = consume(attemptCtx, response)
				timer.Stop()
				if idleExpired.Load() {
					err = failure("network_timeout", nil)
				}
				retry = retryableNetworkError(err)
			case http.StatusTooManyRequests:
				err = failure("rate_limited", nil)
				var delayErr error
				delay, delayErr = retryAfter(response.Header.Get("Retry-After"), attempt)
				retry = delayErr == nil
			case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
				err = failure("source_unavailable", nil)
				retry = true
			case http.StatusUnauthorized:
				err = failure("source_unauthorized", nil)
			case http.StatusForbidden:
				err = failure("source_forbidden", nil)
			case http.StatusNotFound:
				err = failure("release_missing", nil)
			default:
				err = failure("source_http_error", nil)
			}
			// A CDN may reject a short-lived redirect target after it expires.
			// Every attempt starts at resolved (the stable, signed source URL),
			// so it can obtain a fresh redirect. Direct permanent failures retain
			// their classification and do not retry; this shares the same budget.
			if (response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound) && response.Request != nil && response.Request.Response != nil {
				retry = true
			}
			_ = response.Body.Close()
		}
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil {
			return nil
		}
		if !retry || attempt == maxHTTPAttempts-1 {
			return err
		}
		if err := waitForRetry(ctx, delay); err != nil {
			return err
		}
	}
	return failure("source_unavailable", nil)
}
