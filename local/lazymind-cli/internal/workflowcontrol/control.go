// Package workflowcontrol forwards explicit panel actions to an existing DSH Web session.
package workflowcontrol

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"lazymind/agentconnector/internal/localfile"
)

type Binding struct {
	WorkflowSessionID string `json:"workflow_session_id"`
	SessionID         string `json:"session_id"`
	ServerURL         string `json:"server_url"`
	DSHURL            string `json:"dsh_url"`
	AwaitReview       bool   `json:"await_review,omitempty"`
}

type Command struct {
	Action    string `json:"action"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func bindingPath(home, server, run string) string {
	key := sha256.Sum256([]byte(strings.TrimRight(server, "/") + "\x00" + run))
	return filepath.Join(home, "workflow-controls", hex.EncodeToString(key[:])+".json")
}

func Save(home string, binding Binding) error {
	if binding.WorkflowSessionID == "" || binding.SessionID == "" || binding.ServerURL == "" {
		return errors.New("incomplete Workflow controller binding")
	}
	if strings.TrimSpace(binding.DSHURL) != "" {
		if _, err := endpoint(binding.DSHURL); err != nil {
			return err
		}
	}
	path := bindingPath(home, binding.ServerURL, binding.WorkflowSessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	unlock, err := localfile.Lock(path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	if old, err := Load(home, binding.ServerURL, binding.WorkflowSessionID); err == nil {
		if old.SessionID != binding.SessionID {
			return errors.New("Workflow already belongs to another DSH session")
		}
		if strings.TrimSpace(binding.DSHURL) == "" {
			binding.DSHURL = old.DSHURL
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	body, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".binding-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(body)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return localfile.Replace(file.Name(), path)
}

func Load(home, server, run string) (Binding, error) {
	body, err := os.ReadFile(bindingPath(home, server, run))
	if err != nil {
		return Binding{}, err
	}
	var binding Binding
	if err := json.Unmarshal(body, &binding); err != nil {
		return Binding{}, err
	}
	if binding.WorkflowSessionID != run || strings.TrimRight(binding.ServerURL, "/") != strings.TrimRight(server, "/") {
		return Binding{}, errors.New("Workflow controller binding mismatch")
	}
	return binding, nil
}

const defaultLocalDSHURL = "http://127.0.0.1:3080/"

// DiscoverURL finds the DSH root without requiring process environment: bound
// files first, then the local web default. Env still wins when the caller passes it.
func DiscoverURL(home, configured string) (string, error) {
	if configured = strings.TrimSpace(configured); configured != "" {
		if _, err := endpoint(configured); err != nil {
			return "", err
		}
		return configured, nil
	}
	if fromFile := latestBindingURL(home); fromFile != "" {
		if _, err := endpoint(fromFile); err != nil {
			return "", err
		}
		return fromFile, nil
	}
	if _, err := endpoint(defaultLocalDSHURL); err != nil {
		return "", err
	}
	return defaultLocalDSHURL, nil
}

func latestBindingURL(home string) string {
	dir := filepath.Join(home, "workflow-controls")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	newest := ""
	var newestInfo os.FileInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var binding Binding
		if json.Unmarshal(body, &binding) != nil || strings.TrimSpace(binding.DSHURL) == "" {
			continue
		}
		if newestInfo == nil || info.ModTime().After(newestInfo.ModTime()) {
			newestInfo = info
			newest = binding.DSHURL
		}
	}
	return newest
}

func SetAwaitReview(home, server, run string, await bool) error {
	binding, err := Load(home, server, run)
	if err != nil {
		return err
	}
	binding.AwaitReview = await
	return Save(home, binding)
}

// ResolveControlURL uses the binding's DSH root when present, otherwise discovers
// it from Bridge env, earlier bindings, or the local DSH web default.
func ResolveControlURL(home string, binding Binding, configured string) (string, error) {
	bound := strings.TrimSpace(binding.DSHURL)
	if bound != "" {
		if _, err := endpoint(bound); err != nil {
			return "", err
		}
		return bound, nil
	}
	return DiscoverURL(home, configured)
}

// ValidateEndpoint rejects credentials and non-local unencrypted DSH addresses.
func ValidateEndpoint(raw string) error { _, err := endpoint(raw); return err }

func endpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("configure the DSH authenticated root URL")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback()))) {
		return nil, errors.New("DSH requires HTTPS or a loopback HTTP address")
	}
	if u.RawQuery != "" {
		return nil, errors.New("DSH binding URL must not contain credentials or query parameters")
	}
	return u, nil
}

// Forward acknowledges admission, not completion. Requests are never automatically replayed.
func Forward(ctx context.Context, client *http.Client, binding Binding, command Command, loginToken string) error {
	if command.Action != "prompt" && command.Action != "cancel" {
		return errors.New("unsupported Workflow control action")
	}
	if command.RequestID == "" || (command.Action == "prompt" && strings.TrimSpace(command.Message) == "") {
		return errors.New("request ID and prompt message are required")
	}
	root, err := endpoint(binding.DSHURL)
	if err != nil {
		return err
	}
	cookie, cookieErr := BrowserSessionCookie(dshHome(), root.Host)
	if cookie == "" {
		if strings.TrimSpace(loginToken) != "" {
			query := url.Values{"token": []string{loginToken}}
			root.RawQuery = query.Encode()
		}
		login, err := http.NewRequestWithContext(ctx, http.MethodGet, root.String(), nil)
		if err != nil {
			return errors.New("invalid DSH login request")
		}
		response, err := client.Do(login)
		if err != nil {
			return errors.New("DSH is unavailable; check its address and login token")
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			if cookieErr != nil && !errors.Is(cookieErr, os.ErrNotExist) {
				return fmt.Errorf("DSH login failed; %w", cookieErr)
			}
			if strings.TrimSpace(loginToken) == "" {
				return errors.New("DSH login failed; open DSH Web once so it can store a local browser session")
			}
			return errors.New("DSH login failed; update its login token")
		}
	}
	request := map[string]any{"sessionId": binding.SessionID}
	if command.Action == "prompt" {
		request["requestId"] = command.RequestID
		request["mode"] = "queue"
		request["content"] = []map[string]string{{"type": "text", "text": fmt.Sprintf("Workflow session_id=%s\n%s\nRead this Workflow's latest state and artifacts through MCP before acting. Apply this operation only to this run; do not create a new Workflow run.", binding.WorkflowSessionID, command.Message)}}
	}
	method := "session/" + command.Action
	body, err := json.Marshal(map[string]any{"type": "client-request", "rpcId": command.RequestID, "method": method, "payload": map[string]any{"args": map[string]any{"request": request}}})
	if err != nil {
		return err
	}
	root.RawQuery = ""
	root.Path = "/api/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, root.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	response, err := client.Do(req)
	if err != nil {
		return errors.New("DSH control delivery failed; check the original session before retrying")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("DSH control returned HTTP %d", response.StatusCode)
	}
	var result struct {
		RpcID  string `json:"rpcId"`
		Result struct {
			OK    bool `json:"ok"`
			Value struct {
				Accepted bool `json:"accepted"`
			} `json:"value"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return errors.New("invalid DSH control response")
	}
	if result.RpcID != command.RequestID {
		return errors.New("DSH response request ID mismatch")
	}
	if !result.Result.OK {
		return fmt.Errorf("DSH rejected the operation: %s", result.Result.Error.Message)
	}
	if !result.Result.Value.Accepted {
		return errors.New("DSH did not acknowledge the operation")
	}
	return nil
}
