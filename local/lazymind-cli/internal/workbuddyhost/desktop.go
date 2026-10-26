package workbuddyhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DesktopStatus distinguishes an absent endpoint from an occupied, incompatible
// port. Never restart a desktop to try to take over another app's listener.
func desktopStatus(ctx context.Context, port int) (bool, error) {
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return false, nil
	}
	conn.Close()
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("CDP redirect refused") }}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/json/list", nil)
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("WorkBuddy control port %d is occupied: %w", port, err)
	}
	defer resp.Body.Close()
	var targets []struct {
		Type  string `json:"type"`
		Title string `json:"title"`
		URL   string `json:"url"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&targets) != nil {
		return false, fmt.Errorf("WorkBuddy control port %d is occupied by another service", port)
	}
	count := 0
	for _, t := range targets {
		u, err := url.Parse(t.URL)
		if err != nil || u.Scheme != "file" || !strings.HasSuffix(u.Path, "/renderer/index.html") {
			continue
		}
		kind := u.Query().Get("windowAppId")
		if kind == "" {
			kind = u.Query().Get("windowKind")
		}
		if kind != "" && kind != "main" {
			continue
		}
		if t.Type == "page" && strings.EqualFold(t.Title, "WorkBuddy") {
			count++
		}
	}
	if count != 1 {
		return false, fmt.Errorf("expected one WorkBuddy window on port %d, found %d", port, count)
	}
	return true, nil
}

func PrepareDesktop(ctx context.Context, port int, reloadMCP bool) error {
	if port < 1 || port > 65535 {
		return errors.New("invalid WorkBuddy control port")
	}
	ready, err := desktopStatus(ctx, port)
	if err != nil {
		return err
	}
	if ready && !reloadMCP {
		return nil
	}
	application, err := desktopApplication()
	if err != nil {
		return err
	}
	if err := launchDesktop(ctx, application, port); err != nil {
		return err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ready, _ := desktopStatus(ctx, port); ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("WorkBuddy startup did not finish: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func WaitReady(ctx context.Context, home, account string, since time.Time) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		config, pair, err := Load(home, account)
		if err != nil {
			return err
		}
		if status, err := ReadStatus(config, pair.ConnectorID); err == nil && status.CheckedAt >= since.UnixMilli() {
			// Transport/startup failures may recover; explicit compatibility failures do not.
			if issue := StatusIssue(status); issue != "" && issue != "host_unavailable" {
				return &CompatibilityError{Code: issue}
			}
			if status.Ready && status.Cancellation == "session" {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return &CompatibilityError{Code: "host_unavailable"}
		case <-ticker.C:
		}
	}
}
