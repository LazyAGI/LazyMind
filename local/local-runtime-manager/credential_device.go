package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/storagepath"
)

const cloudCredentialKeyEnvVar = "LAZYMIND_AUTH_CLOUD_SECRET_KEY"
const legacyCloudCredentialKey = "dev-ragscan-secret-key-change-me"
const modelCredentialKeyEnvVar = "LAZYMIND_MODEL_PROVIDER_SECRET_KEY"

type credentialDeviceIdentity struct {
	Version      int    `json:"version"`
	DeviceID     string `json:"deviceId"`
	DeviceSecret string `json:"deviceSecret"`
}

func cloudCredentialKey() (string, error) {
	return cloudCredentialKeyForPaths(RuntimePaths{})
}

func cloudCredentialKeyForPaths(paths RuntimePaths) (string, error) {
	if configured := strings.TrimSpace(os.Getenv(cloudCredentialKeyEnvVar)); configured != "" && configured != legacyCloudCredentialKey {
		return configured, nil
	}
	return deviceCredentialKeyForPaths("cloud-oauth", paths)
}

func modelCredentialKey() (string, error) {
	return modelCredentialKeyForPaths(RuntimePaths{})
}

func modelCredentialKeyForPaths(paths RuntimePaths) (string, error) {
	if configured := strings.TrimSpace(os.Getenv(modelCredentialKeyEnvVar)); configured != "" {
		return configured, nil
	}
	return deviceCredentialKeyForPaths("model-provider", paths)
}

func deviceCredentialKey(purpose string) (string, error) {
	return deviceCredentialKeyForPaths(purpose, RuntimePaths{})
}

func deviceCredentialKeyForPaths(purpose string, paths RuntimePaths) (string, error) {
	path, err := checkedCredentialIdentityPath(paths)
	if err != nil {
		return "", err
	}
	identity, err := loadOrCreateCredentialDevice(path)
	if err != nil {
		return "", err
	}
	secret, err := decodeCredentialDeviceSecret(identity)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(mac, "lazymind/desktop/%s/%s/v1", identity.DeviceID, purpose)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func credentialDeviceIdentityPath() string {
	path, _, _ := credentialIdentityLocation(RuntimePaths{})
	return path
}

func credentialIdentityLocation(paths RuntimePaths) (string, string, error) {
	root, installed, err := storagepath.Resolve(paths.DesktopAppDir)
	if err != nil {
		return "", "", err
	}
	if installed {
		return filepath.Join(root, "Desktop", "credential-device.json"), root, nil
	}
	return credentialDeviceIdentityPathForOS(runtime.GOOS, hostHomeDir(), os.Getenv("LOCALAPPDATA")),
		defaultRuntimePathLayout().DataRoot, nil
}

// checkedCredentialIdentityPath is also the preflight boundary used before
// Electron creates its shared identity. It never creates an identity or store.
func checkedCredentialIdentityPath(paths RuntimePaths) (string, error) {
	path, root, err := credentialIdentityLocation(paths)
	if err != nil {
		return "", err
	}
	if _, err := readCredentialDevice(path); err == nil {
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	roots := []string{root, firstNonEmpty(paths.RuntimeRoot, root)}
	seen := map[string]bool{}
	for index, dataRoot := range roots {
		sqliteRoot := filepath.Join(dataRoot, "data", "stores", "sqlite")
		if index == 1 && paths.RuntimeRoot != "" {
			sqliteRoot = envText(localSQLiteDirEnvVar, sqliteRoot)
		}
		authPath := filepath.Join(sqliteRoot, "auth", "authservice.db")
		corePath := filepath.Join(sqliteRoot, "core", "core.db")
		if index == 1 {
			authPath = firstNonEmpty(paths.AuthServiceDBPath, authPath)
			corePath = firstNonEmpty(paths.CoreDBPath, corePath)
		}
		for _, store := range []struct {
			path   string
			tables map[string][]string
		}{
			{authPath, map[string][]string{
				"cloud_auth_connections": {"credential_ciphertext", "auth_state_ciphertext"},
				"mcp_oauth_grants":       {"ciphertext"}, "mcp_oauth_states": {"ciphertext"},
			}},
			{corePath, map[string][]string{
				"user_model_provider_groups": {"api_key_ciphertext"}, "mcp_servers": {"headers_json"},
			}},
		} {
			if seen[store.path] {
				continue
			}
			seen[store.path] = true
			found, err := storedDeviceCredentials(store.path, store.tables)
			if err != nil {
				return "", fmt.Errorf("inspect credentials before creating a device identity: %w", err)
			}
			if found {
				return "", fmt.Errorf("credential device identity is missing while encrypted credentials remain; restore the original identity")
			}
		}
	}
	return path, nil
}

func storedDeviceCredentials(path string, tables map[string][]string) (bool, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	uriPath := filepath.ToSlash(path)
	if runtime.GOOS == "windows" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return false, err
	}
	defer db.Close()
	for table, fields := range tables {
		rows, err := db.Query("PRAGMA table_info(" + table + ")")
		if err != nil {
			return false, err
		}
		columns := map[string]bool{}
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, kind string
			var defaultValue any
			if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
				rows.Close()
				return false, err
			}
			columns[name] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return false, err
		}
		for _, field := range fields {
			if !columns[field] {
				continue
			}
			predicate := "TRIM(COALESCE(" + field + ", '')) <> ''"
			if field == "headers_json" {
				predicate = "CASE WHEN json_valid(headers_json) THEN json_extract(headers_json, '$.enc') = 'aes-gcm' ELSE 0 END"
			}
			var found bool
			if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM " + table + " WHERE " + predicate + ")").Scan(&found); err != nil {
				return false, err
			}
			if found {
				return true, nil
			}
		}
	}
	return false, nil
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
