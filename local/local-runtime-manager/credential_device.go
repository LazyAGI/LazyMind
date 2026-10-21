package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const cloudCredentialKeyEnvVar = "LAZYMIND_AUTH_CLOUD_SECRET_KEY"
const legacyCloudCredentialKey = "dev-ragscan-secret-key-change-me"

type credentialDeviceIdentity struct {
	Version      int    `json:"version"`
	DeviceID     string `json:"deviceId"`
	DeviceSecret string `json:"deviceSecret"`
}

func cloudCredentialKey() (string, error) {
	if configured := strings.TrimSpace(os.Getenv(cloudCredentialKeyEnvVar)); configured != "" && configured != legacyCloudCredentialKey {
		return configured, nil
	}
	identity, err := loadOrCreateCredentialDevice(credentialDeviceIdentityPath())
	if err != nil {
		return "", err
	}
	secret, err := decodeCredentialDeviceSecret(identity)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(mac, "lazymind/desktop/%s/cloud-oauth/v1", identity.DeviceID)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func credentialDeviceIdentityPath() string {
	return credentialDeviceIdentityPathForOS(runtime.GOOS, hostHomeDir(), os.Getenv("LOCALAPPDATA"))
}

func credentialDeviceIdentityPathForOS(goos, home, localAppData string) string {
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "lazymind-desktop", "credential-device.json")
	case "windows":
		root := strings.TrimSpace(localAppData)
		if root == "" {
			root = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(root, "LazyMind", "Desktop", "credential-device.json")
	default:
		return filepath.Join(home, ".config", "lazymind-desktop", "credential-device.json")
	}
}

func readCredentialDevice(path string) (credentialDeviceIdentity, error) {
	var identity credentialDeviceIdentity
	raw, err := os.ReadFile(path)
	if err != nil {
		return identity, fmt.Errorf("read credential device identity: %w", err)
	}
	if err := json.Unmarshal(raw, &identity); err != nil {
		return identity, fmt.Errorf("invalid credential device identity")
	}
	if _, err := decodeCredentialDeviceSecret(identity); err != nil {
		return identity, err
	}
	return identity, nil
}

func decodeCredentialDeviceSecret(identity credentialDeviceIdentity) ([]byte, error) {
	secret, err := base64.RawURLEncoding.DecodeString(identity.DeviceSecret)
	if err != nil {
		secret, err = base64.URLEncoding.DecodeString(identity.DeviceSecret)
	}
	if identity.Version != 1 || strings.TrimSpace(identity.DeviceID) == "" || err != nil || len(secret) != 32 {
		return nil, fmt.Errorf("invalid credential device identity")
	}
	return secret, nil
}

func loadOrCreateCredentialDevice(path string) (credentialDeviceIdentity, error) {
	identity, err := readCredentialDevice(path)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return identity, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return identity, fmt.Errorf("create credential device directory: %w", err)
	}
	var id [16]byte
	var secret [32]byte
	if _, err := rand.Read(id[:]); err != nil {
		return identity, fmt.Errorf("generate credential device identity: %w", err)
	}
	if _, err := rand.Read(secret[:]); err != nil {
		return identity, fmt.Errorf("generate credential device identity: %w", err)
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	identity = credentialDeviceIdentity{
		Version:      1,
		DeviceID:     fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:]),
		DeviceSecret: base64.RawURLEncoding.EncodeToString(secret[:]),
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return identity, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".credential-device-*.tmp")
	if err != nil {
		return identity, fmt.Errorf("create credential device identity: %w", err)
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(append(raw, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		return identity, fmt.Errorf("write credential device identity: %w", writeErr)
	}
	if closeErr != nil {
		return identity, fmt.Errorf("close credential device identity: %w", closeErr)
	}
	// Publish a complete file without replacing an identity created by another process.
	if err := os.Link(file.Name(), path); err != nil && !os.IsExist(err) {
		return identity, fmt.Errorf("publish credential device identity: %w", err)
	}
	return readCredentialDevice(path)
}
