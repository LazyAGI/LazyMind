package workflowcontrol

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	dshBrowserSessionKey = "client-connection/browser-session"
	dshCookiePrefix      = "dsh-auth-"
	dshCookieMaxAge      = 30 * 24 * time.Hour
	dshSecretBytes       = 32
)

type dshCredentialsFile struct {
	Records map[string]dshCredentialRecord `yaml:"records"`
}

type dshCredentialRecord struct {
	Kind    string `yaml:"kind"`
	Payload struct {
		Version int    `yaml:"version"`
		Secret  string `yaml:"secret"`
	} `yaml:"payload"`
}

type dshBrowserCookiePayload struct {
	Version   int    `json:"version"`
	Authority string `json:"authority"`
	IssuedAt  int64  `json:"issuedAt"`
	ExpiresAt int64  `json:"expiresAt"`
}

func dshHome() string {
	if configured := strings.TrimSpace(os.Getenv("DSH_HOME")); configured != "" {
		return configured
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".dsh")
}

// BrowserSessionCookie mints the same authority-bound cookie DSH Web would set
// after a launch-token exchange, using the durable signing secret in DSH home.
func BrowserSessionCookie(home, authority string) (string, error) {
	if strings.TrimSpace(home) == "" || strings.TrimSpace(authority) == "" {
		return "", errors.New("DSH home and authority are required")
	}
	body, err := os.ReadFile(filepath.Join(home, ".credentials.yaml"))
	if err != nil {
		return "", err
	}
	var file dshCredentialsFile
	if err := yaml.Unmarshal(body, &file); err != nil {
		return "", errors.New("invalid DSH credentials file")
	}
	record, ok := file.Records[dshBrowserSessionKey]
	if !ok || record.Kind != "grant" || record.Payload.Version != 1 {
		return "", errors.New("DSH browser-session credential is missing")
	}
	secret, err := decodeDSHSecret(record.Payload.Secret)
	if err != nil {
		return "", err
	}
	issuedAt := time.Now().UnixMilli()
	payload, err := json.Marshal(dshBrowserCookiePayload{
		Version: 1, Authority: authority, IssuedAt: issuedAt, ExpiresAt: issuedAt + dshCookieMaxAge.Milliseconds(),
	})
	if err != nil {
		return "", err
	}
	encoded := encodeDSHBase64(payload)
	signature := encodeDSHBase64(hmacSHA256(secret, encoded))
	name := dshCookiePrefix + encodeDSHBase64(sha256Sum([]byte(authority)))
	return name + "=v1." + encoded + "." + signature, nil
}

func decodeDSHSecret(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != dshSecretBytes {
		return nil, errors.New("DSH browser-session credential is invalid")
	}
	if encodeDSHBase64(decoded) != value {
		return nil, errors.New("DSH browser-session credential is invalid")
	}
	return decoded, nil
}

func encodeDSHBase64(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}

func sha256Sum(value []byte) []byte {
	sum := sha256.Sum256(value)
	return sum[:]
}

func hmacSHA256(secret []byte, body string) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(body))
	return mac.Sum(nil)
}
