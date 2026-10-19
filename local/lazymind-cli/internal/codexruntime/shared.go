// Package codexruntime reads the shared Desktop registration used by both
// Workflow binding and the Node dispatcher. Missing registration means queue;
// a malformed registration must never silently advertise legacy capabilities.
package codexruntime

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const RegistrationName = "lazymind-shared-server.json"

// SameProfile preserves pairing IDs while accepting aliases of the same directory.
func SameProfile(a, b string) bool {
	if !filepath.IsAbs(a) || !filepath.IsAbs(b) {
		return false
	}
	if a == b {
		return true
	}
	left, err := os.Stat(a)
	if err != nil || !left.IsDir() {
		return false
	}
	right, err := os.Stat(b)
	return err == nil && right.IsDir() && os.SameFile(left, right)
}

type Registration struct {
	Installation string `json:"installation,omitempty"`
	Version      int    `json:"version"`
	Profile      string `json:"profile"`
	Endpoint     string `json:"app_server_url"`
	TokenFile    string `json:"auth_token_file,omitempty"`
}

func Read(profile string) (*Registration, error) {
	file := filepath.Join(profile, RegistrationName)
	info, err := os.Lstat(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || checkPrivate(file) != nil {
		return nil, errors.New("shared server registration must be a private owned file")
	}
	body, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var value Registration
	if json.Unmarshal(body, &value) != nil || value.Version != 1 || !SameProfile(value.Profile, profile) {
		return nil, errors.New("shared server registration does not match this Codex profile")
	}
	if err := validateEndpoint(value.Endpoint, value.TokenFile); err != nil {
		return nil, err
	}
	if value.TokenFile != "" {
		if err := checkPrivate(value.TokenFile); err != nil {
			return nil, err
		}
	}
	return &value, nil
}

func validateEndpoint(endpoint, tokenFile string) error {
	if strings.HasPrefix(endpoint, "ws://") {
		match := regexp.MustCompile(`^ws://127\.0\.0\.1:([1-9][0-9]{0,4})$`).FindStringSubmatch(endpoint)
		if len(match) != 2 || !filepath.IsAbs(tokenFile) {
			return errors.New("loopback endpoint requires a private token file")
		}
		port, _ := strconv.Atoi(match[1])
		if port > 65535 {
			return errors.New("invalid loopback port")
		}
		return nil
	}
	if tokenFile != "" {
		return errors.New("Unix endpoint cannot use a TCP token")
	}
	const prefix = "ws+unix://localhost"
	if !strings.HasPrefix(endpoint, prefix+"/") || !strings.HasSuffix(endpoint, ":/rpc") {
		return errors.New("shared server endpoint must be a local Unix WebSocket")
	}
	encoded := strings.TrimSuffix(strings.TrimPrefix(endpoint, prefix), ":/rpc")
	if strings.ContainsAny(encoded, "?#") {
		return errors.New("invalid shared server endpoint")
	}
	socket, err := url.PathUnescape(encoded)
	if err != nil || !filepath.IsAbs(socket) {
		return errors.New("invalid shared server socket path")
	}
	for _, char := range socket {
		if char < 32 || char == ':' {
			return errors.New("invalid shared server socket path")
		}
	}
	return nil
}

func Cancellation(profile string) (string, error) {
	registration, err := Read(profile)
	if err != nil {
		return "", err
	}
	if registration != nil {
		return "session", nil
	}
	return "none", nil
}
