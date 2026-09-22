package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"lazymind/core/common"
	coreproviderconnection "lazymind/core/providerconnection"
)

type sidecarServer struct {
	backend *coreproviderconnection.FeishuCLIDeviceFlowCoordinator
	hmacKey []byte
	now     func() time.Time

	nonceMu sync.Mutex
	nonces  map[string]time.Time
}

func main() {
	if err := run(); err != nil {
		log.Printf("Feishu CLI sidecar startup failed")
		os.Exit(1)
	}
}

func run() error {
	binaryPath := strings.TrimSpace(os.Getenv("LAZYMIND_FEISHU_CLI_PATH"))
	binarySHA256, err := readSecretFile(os.Getenv("LAZYMIND_FEISHU_CLI_SHA256_FILE"), 64, 128)
	if err != nil {
		return err
	}
	runner, err := coreproviderconnection.NewFeishuCLIRunner(binaryPath, string(binarySHA256))
	clearBytes(binarySHA256)
	if err != nil {
		return err
	}
	profiles, err := coreproviderconnection.NewFeishuCLIProfileStore(os.Getenv("LAZYMIND_FEISHU_CLI_RUNTIME_ROOT"))
	if err != nil {
		return err
	}
	internalToken, err := readSecretFile(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN_FILE"), 16, 4096)
	if err != nil {
		return err
	}
	registry := coreproviderconnection.HTTPRegistry{
		BaseURL:       sidecarAuthServiceBaseURL(),
		InternalToken: strings.TrimSpace(string(internalToken)),
	}
	clearBytes(internalToken)
	coordinator, err := coreproviderconnection.NewFeishuCLIDeviceFlowCoordinator(
		runner, profiles, registry, coreproviderconnection.DefaultFeishuCLIReadScopes,
	)
	if err != nil {
		return err
	}
	if err := coordinator.UseCredentialLocation("cli_sidecar"); err != nil {
		return err
	}
	hmacKey, err := readSecretFile(os.Getenv("LAZYMIND_FEISHU_CLI_SIDECAR_HMAC_KEY_FILE"), 32, 4096)
	if err != nil {
		return err
	}
	server := &sidecarServer{backend: coordinator, hmacKey: hmacKey, now: time.Now, nonces: map[string]time.Time{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("POST /v1/sessions:start", server.signed(server.start))
	mux.HandleFunc("POST /v1/sessions:get", server.signed(server.get))
	mux.HandleFunc("POST /v1/sessions:cancel", server.signed(server.cancel))
	mux.HandleFunc("POST /v1/execute", server.signed(server.execute))
	listenAddress := strings.TrimSpace(os.Getenv("LAZYMIND_FEISHU_CLI_SIDECAR_LISTEN"))
	if listenAddress == "" {
		listenAddress = "0.0.0.0:19091"
	}
	return (&http.Server{
		Addr: listenAddress, Handler: mux, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 3 * time.Minute, IdleTimeout: time.Minute,
	}).ListenAndServe()
}

func sidecarAuthServiceBaseURL() string {
	return common.AuthServiceBaseURL()
}

func (server *sidecarServer) signed(next func(http.ResponseWriter, *http.Request, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, request.Body, 64<<10))
		if err != nil {
			http.Error(w, "invalid request", http.StatusUnprocessableEntity)
			return
		}
		timestamp := request.Header.Get("X-LazyMind-CLI-Timestamp")
		nonce := request.Header.Get("X-LazyMind-CLI-Nonce")
		signature := request.Header.Get("X-LazyMind-CLI-Signature")
		if coreproviderconnection.VerifyFeishuCLISidecarRequest(
			server.hmacKey, request.Method, request.URL.Path, timestamp, nonce, signature, body, server.now(),
		) != nil || !server.consumeNonce(nonce) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, request, body)
	}
}

func (server *sidecarServer) consumeNonce(nonce string) bool {
	server.nonceMu.Lock()
	defer server.nonceMu.Unlock()
	now := server.now()
	for value, expiresAt := range server.nonces {
		if !expiresAt.After(now) {
			delete(server.nonces, value)
		}
	}
	if _, exists := server.nonces[nonce]; exists {
		return false
	}
	server.nonces[nonce] = now.Add(time.Minute)
	return true
}

func (server *sidecarServer) start(w http.ResponseWriter, request *http.Request, body []byte) {
	var input struct {
		OwnerUserID             string `json:"owner_user_id"`
		ReauthorizeConnectionID string `json:"reauthorize_connection_id"`
	}
	if decode(body, &input) != nil {
		http.Error(w, "invalid request", http.StatusUnprocessableEntity)
		return
	}
	session, err := server.backend.Start(request.Context(), input.OwnerUserID, input.ReauthorizeConnectionID)
	writeJSON(w, session, err)
}

func (server *sidecarServer) get(w http.ResponseWriter, request *http.Request, body []byte) {
	var input struct {
		OwnerUserID string `json:"owner_user_id"`
		SessionID   string `json:"session_id"`
	}
	if decode(body, &input) != nil {
		http.Error(w, "invalid request", http.StatusUnprocessableEntity)
		return
	}
	session, err := server.backend.Get(request.Context(), input.OwnerUserID, input.SessionID)
	writeJSON(w, session, err)
}

func (server *sidecarServer) cancel(w http.ResponseWriter, request *http.Request, body []byte) {
	var input struct {
		OwnerUserID string `json:"owner_user_id"`
		SessionID   string `json:"session_id"`
	}
	if decode(body, &input) != nil {
		http.Error(w, "invalid request", http.StatusUnprocessableEntity)
		return
	}
	if err := server.backend.Cancel(request.Context(), input.OwnerUserID, input.SessionID); err != nil {
		http.Error(w, "Feishu CLI session is unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (server *sidecarServer) execute(w http.ResponseWriter, request *http.Request, body []byte) {
	var input struct {
		OwnerUserID  string            `json:"owner_user_id"`
		ConnectionID string            `json:"connection_id"`
		ProfileRef   string            `json:"profile_ref"`
		Operation    string            `json:"operation"`
		Params       map[string]string `json:"params"`
	}
	if decode(body, &input) != nil || len(input.Params) > 16 {
		http.Error(w, "invalid request", http.StatusUnprocessableEntity)
		return
	}
	result, err := server.backend.Execute(
		request.Context(), input.OwnerUserID, input.ConnectionID, input.ProfileRef, input.Operation, input.Params,
	)
	writeJSON(w, result, err)
}

func decode(payload []byte, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("multiple JSON values")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, value any, err error) {
	if err != nil {
		http.Error(w, "Feishu CLI operation is unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}

func readSecretFile(rawPath string, minimum, maximum int64) ([]byte, error) {
	path := filepath.Clean(strings.TrimSpace(rawPath))
	if !filepath.IsAbs(path) || path == string(filepath.Separator) {
		return nil, errors.New("secret file is unavailable")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < minimum || info.Size() > maximum {
		return nil, errors.New("secret file is unavailable")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("secret file is unavailable")
	}
	payload = []byte(strings.TrimSpace(string(payload)))
	if int64(len(payload)) < minimum || int64(len(payload)) > maximum {
		return nil, errors.New("secret file is unavailable")
	}
	return payload, nil
}

func clearBytes(payload []byte) {
	for index := range payload {
		payload[index] = 0
	}
}
