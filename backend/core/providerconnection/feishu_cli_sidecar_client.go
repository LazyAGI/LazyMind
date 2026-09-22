package providerconnection

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lazymind/core/cloudclient"
)

type FeishuCLISidecarClient struct {
	baseURL    *url.URL
	hmacKey    []byte
	httpClient *http.Client
	now        func() time.Time
}

func NewFeishuCLISidecarClient(baseURL string, hmacKey []byte, client *http.Client) (*FeishuCLISidecarClient, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || len(hmacKey) < 32 {
		return nil, ErrCLIUnavailable
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, ErrCLIUnavailable
	}
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	return &FeishuCLISidecarClient{
		baseURL: parsed, hmacKey: append([]byte(nil), hmacKey...), httpClient: client, now: time.Now,
	}, nil
}

func (client *FeishuCLISidecarClient) Start(ctx context.Context, ownerUserID, reauthorizeConnectionID string) (cloudclient.ProviderConnectionSession, error) {
	var session cloudclient.ProviderConnectionSession
	err := client.post(ctx, "/v1/sessions:start", map[string]string{
		"owner_user_id": ownerUserID, "reauthorize_connection_id": reauthorizeConnectionID,
	}, &session)
	return session, err
}

func (client *FeishuCLISidecarClient) Get(ctx context.Context, ownerUserID, sessionID string) (cloudclient.ProviderConnectionSession, error) {
	var session cloudclient.ProviderConnectionSession
	err := client.post(ctx, "/v1/sessions:get", map[string]string{"owner_user_id": ownerUserID, "session_id": sessionID}, &session)
	return session, err
}

func (client *FeishuCLISidecarClient) Cancel(ctx context.Context, ownerUserID, sessionID string) error {
	return client.post(ctx, "/v1/sessions:cancel", map[string]string{"owner_user_id": ownerUserID, "session_id": sessionID}, nil)
}

func (client *FeishuCLISidecarClient) Execute(ctx context.Context, ownerUserID, connectionID, profileReference, operation string, params map[string]string) (FeishuCLIExecutionResult, error) {
	var result FeishuCLIExecutionResult
	err := client.post(ctx, "/v1/execute", map[string]any{
		"owner_user_id": ownerUserID, "connection_id": connectionID,
		"profile_ref": profileReference, "operation": operation, "params": params,
	}, &result)
	return result, err
}

func (client *FeishuCLISidecarClient) post(ctx context.Context, path string, input, output any) error {
	if client == nil || client.baseURL == nil || !strings.HasPrefix(path, "/v1/") {
		return ErrCLIUnavailable
	}
	body, err := json.Marshal(input)
	if err != nil || len(body) > 64<<10 {
		return ErrCLIOutputInvalid
	}
	target := *client.baseURL
	target.Path = strings.TrimRight(target.Path, "/") + path
	timestamp := strconv.FormatInt(client.now().UTC().Unix(), 10)
	nonceBytes := make([]byte, 24)
	if _, err := io.ReadFull(rand.Reader, nonceBytes); err != nil {
		return ErrCLIUnavailable
	}
	nonce := hex.EncodeToString(nonceBytes)
	signature := SignFeishuCLISidecarRequest(client.hmacKey, http.MethodPost, path, timestamp, nonce, body)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return ErrCLIUnavailable
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-LazyMind-CLI-Timestamp", timestamp)
	request.Header.Set("X-LazyMind-CLI-Nonce", nonce)
	request.Header.Set("X-LazyMind-CLI-Signature", signature)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return ErrCLIUnavailable
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 140<<20))
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return ErrCLIUnavailable
	}
	if output == nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return ErrCLIOutputInvalid
	}
	return nil
}

func SignFeishuCLISidecarRequest(key []byte, method, path, timestamp, nonce string, body []byte) string {
	digest := sha256.Sum256(body)
	canonical := strings.Join([]string{method, path, timestamp, nonce, hex.EncodeToString(digest[:])}, "\n")
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

func VerifyFeishuCLISidecarRequest(key []byte, method, path, timestamp, nonce, signature string, body []byte, now time.Time) error {
	if len(key) < 32 || len(nonce) != 48 || len(signature) != sha256.Size*2 {
		return errors.New("invalid Feishu CLI sidecar signature")
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || now.Sub(time.Unix(seconds, 0)).Abs() > 30*time.Second {
		return errors.New("expired Feishu CLI sidecar signature")
	}
	expected := SignFeishuCLISidecarRequest(key, method, path, timestamp, nonce, body)
	providedBytes, err := hex.DecodeString(signature)
	if err != nil {
		return errors.New("invalid Feishu CLI sidecar signature")
	}
	expectedBytes, _ := hex.DecodeString(expected)
	if !hmac.Equal(expectedBytes, providedBytes) {
		return errors.New("invalid Feishu CLI sidecar signature")
	}
	return nil
}
