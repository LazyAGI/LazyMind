package codexcontrol

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	remoteControlFeature = "remote_control"
	desktopClientName    = "Codex Desktop"
	defaultChatGPTBase   = "https://chatgpt.com/backend-api"
	proxyMarkerBegin     = "# >>> LazyMind Codex native control >>>"
	proxyMarkerEnd       = "# <<< LazyMind Codex native control <<<"
	proxyOriginalPrefix  = "# original-chatgpt-base-url-line: "
	proxyUpstreamPrefix  = "# upstream-chatgpt-base-url: "
)

var chatGPTBaseLine = regexp.MustCompile(`(?m)^chatgpt_base_url[ \t]*=.*(?:\r?\n|$)`)

type ConfigStatus struct {
	Configured   bool   `json:"configured"`
	WebSocketURL string `json:"websocket_url,omitempty"`
	StateDB      string `json:"state_db,omitempty"`
}

func Configure(address string) (ConfigStatus, error) {
	paths, err := controlPaths(address)
	if err != nil {
		return ConfigStatus{}, err
	}
	db, err := openStateDB(paths.stateDB)
	if err != nil {
		return ConfigStatus{}, err
	}
	defer db.Close()
	if err := ensureControlSchema(db); err != nil {
		return ConfigStatus{}, err
	}
	proxyChanged, err := installProxyConfig(paths.configFile, paths.chatGPTBaseURL)
	if err != nil {
		return ConfigStatus{}, err
	}
	committed := false
	defer func() {
		if proxyChanged && !committed {
			_ = restoreProxyConfig(paths.configFile)
		}
	}()
	transaction, err := db.Begin()
	if err != nil {
		return ConfigStatus{}, err
	}
	defer transaction.Rollback()
	if _, err := transaction.Exec(`
		INSERT INTO local_app_server_feature_enablement (feature_name, enabled, updated_at)
		VALUES (?, 1, ?)
		ON CONFLICT(feature_name) DO UPDATE SET enabled = excluded.enabled, updated_at = excluded.updated_at
	`, remoteControlFeature, time.Now().UnixMilli()); err != nil {
		return ConfigStatus{}, fmt.Errorf("enable Codex native control: %w", err)
	}
	if _, err := transaction.Exec(`
		INSERT INTO remote_control_enrollments (
			websocket_url, account_id, app_server_client_name,
			server_id, environment_id, server_name, remote_control_enabled, updated_at
		) VALUES (?, ?, ?, ?, ?, 'LazyMind', 1, ?)
		ON CONFLICT(websocket_url, account_id, app_server_client_name) DO UPDATE SET
			server_id = excluded.server_id,
			environment_id = excluded.environment_id,
			server_name = excluded.server_name,
			remote_control_enabled = excluded.remote_control_enabled,
			updated_at = excluded.updated_at
	`, paths.websocketURL, paths.accountID, desktopClientName, stableID("srv", paths.installationID),
		stableID("env", paths.installationID), time.Now().Unix()); err != nil {
		return ConfigStatus{}, fmt.Errorf("enroll Codex native control: %w", err)
	}
	if _, err := transaction.Exec(`
		DELETE FROM remote_control_enrollments
		WHERE websocket_url = ? AND account_id = ? AND app_server_client_name = ''
	`, paths.websocketURL, paths.accountID); err != nil {
		return ConfigStatus{}, fmt.Errorf("remove legacy Codex native control enrollment: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return ConfigStatus{}, err
	}
	committed = true
	return InspectConfig(address)
}

func InspectConfig(address string) (ConfigStatus, error) {
	paths, err := controlPaths(address)
	if err != nil {
		return ConfigStatus{}, err
	}
	status := ConfigStatus{WebSocketURL: paths.websocketURL, StateDB: paths.stateDB}
	proxyConfigured, _, err := inspectProxyConfig(paths.configFile, paths.chatGPTBaseURL)
	if err != nil {
		return ConfigStatus{}, err
	}
	db, err := openStateDB(paths.stateDB)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return status, nil
		}
		return ConfigStatus{}, err
	}
	defer db.Close()
	var enabled int
	featureErr := db.QueryRow(
		`SELECT enabled FROM local_app_server_feature_enablement WHERE feature_name = ?`,
		remoteControlFeature,
	).Scan(&enabled)
	if featureErr != nil && !errors.Is(featureErr, sql.ErrNoRows) && !strings.Contains(featureErr.Error(), "no such table") {
		return ConfigStatus{}, featureErr
	}
	var count int
	enrollmentErr := db.QueryRow(`
		SELECT COUNT(*) FROM remote_control_enrollments
		WHERE websocket_url = ? AND account_id = ? AND app_server_client_name = ?
			AND remote_control_enabled = 1
	`, paths.websocketURL, paths.accountID, desktopClientName).Scan(&count)
	if enrollmentErr != nil && !strings.Contains(enrollmentErr.Error(), "no such table") {
		return ConfigStatus{}, enrollmentErr
	}
	status.Configured = proxyConfigured && enabled == 1 && count == 1
	return status, nil
}

func RemoveConfig(address string) error {
	paths, err := controlPaths(address)
	if err != nil {
		return err
	}
	db, err := openStateDB(paths.stateDB)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return restoreProxyConfig(paths.configFile)
		}
		return err
	}
	defer db.Close()
	if _, err := db.Exec(`
		DELETE FROM remote_control_enrollments
		WHERE websocket_url = ? AND account_id = ? AND app_server_client_name IN ('', ?)
	`, paths.websocketURL, paths.accountID, desktopClientName); err != nil && !strings.Contains(err.Error(), "no such table") {
		return err
	}
	return restoreProxyConfig(paths.configFile)
}

type resolvedControlPaths struct {
	stateDB        string
	configFile     string
	websocketURL   string
	chatGPTBaseURL string
	accountID      string
	installationID string
}

func controlPaths(address string) (resolvedControlPaths, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		address = DefaultAddress
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return resolvedControlPaths{}, fmt.Errorf("invalid LazyMind Assistant address: %w", err)
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return resolvedControlPaths{}, errors.New("Codex native control must use a loopback address")
	}
	home, err := codexHome()
	if err != nil {
		return resolvedControlPaths{}, err
	}
	installationID, err := installationID(home)
	if err != nil {
		return resolvedControlPaths{}, err
	}
	accountID, err := accountID(home)
	if err != nil {
		return resolvedControlPaths{}, err
	}
	stateDB := filepath.Join(home, "state_5.sqlite")
	if _, statErr := os.Stat(stateDB); errors.Is(statErr, os.ErrNotExist) {
		legacy := filepath.Join(home, "sqlite", "state_5.sqlite")
		if _, legacyErr := os.Stat(legacy); legacyErr == nil {
			stateDB = legacy
		}
	}
	return resolvedControlPaths{
		stateDB: stateDB, configFile: filepath.Join(home, "config.toml"),
		accountID: accountID, installationID: installationID,
		websocketURL:   "ws://" + address + "/backend-api/wham/remote/control/server",
		chatGPTBaseURL: "http://" + address + "/backend-api",
	}, nil
}

func openStateDB(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func ensureControlSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS local_app_server_feature_enablement (
			feature_name TEXT PRIMARY KEY,
			enabled INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS remote_control_enrollments (
			websocket_url TEXT NOT NULL,
			account_id TEXT NOT NULL,
			app_server_client_name TEXT NOT NULL,
			server_id TEXT NOT NULL,
			environment_id TEXT NOT NULL,
			server_name TEXT NOT NULL,
			remote_control_enabled INTEGER,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (websocket_url, account_id, app_server_client_name)
		);
	`)
	if err != nil {
		return err
	}
	hasEnabled, err := tableHasColumn(db, "remote_control_enrollments", "remote_control_enabled")
	if err != nil || hasEnabled {
		return err
	}
	_, err = db.Exec(`ALTER TABLE remote_control_enrollments ADD COLUMN remote_control_enabled INTEGER`)
	return err
}

func tableHasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func configuredUpstreamURL() (string, error) {
	home, err := codexHome()
	if err != nil {
		return "", err
	}
	_, upstream, err := inspectProxyConfig(filepath.Join(home, "config.toml"), "")
	return upstream, err
}

func installProxyConfig(path, localBaseURL string) (bool, error) {
	body, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	start, end, block := proxyBlock(body)
	if start >= 0 {
		original, upstream, err := proxyBlockMetadata(block)
		if err != nil {
			return false, err
		}
		replacement := buildProxyBlock(localBaseURL, original, upstream)
		if string(block) == replacement {
			return false, nil
		}
		return true, writeConfig(path, append(append([]byte{}, body[:start]...), append([]byte(replacement), body[end:]...)...))
	}
	original := []byte(nil)
	upstream := defaultChatGPTBase
	if location := chatGPTBaseLine.FindIndex(body); location != nil {
		original = append(original, body[location[0]:location[1]]...)
		upstream, err = parseChatGPTBaseLine(string(original))
		if err != nil {
			return false, err
		}
		replacement := []byte(buildProxyBlock(localBaseURL, original, upstream))
		body = append(append(append([]byte{}, body[:location[0]]...), replacement...), body[location[1]:]...)
	} else {
		body = append([]byte(buildProxyBlock(localBaseURL, nil, upstream)), body...)
	}
	return true, writeConfig(path, body)
}

func restoreProxyConfig(path string) error {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	restored, err := restoredProxyConfig(body)
	if err != nil || restored == nil {
		return err
	}
	return writeConfig(path, restored)
}

func restoredProxyConfig(body []byte) ([]byte, error) {
	start, end, block := proxyBlock(body)
	if start < 0 {
		return nil, nil
	}
	original, _, err := proxyBlockMetadata(block)
	if err != nil {
		return nil, err
	}
	restored := append(append([]byte{}, body[:start]...), append(original, body[end:]...)...)
	return restored, nil
}

func inspectProxyConfig(path, expectedLocalURL string) (bool, string, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, defaultChatGPTBase, nil
	}
	if err != nil {
		return false, "", err
	}
	_, _, block := proxyBlock(body)
	if block != nil {
		_, upstream, err := proxyBlockMetadata(block)
		if err != nil {
			return false, "", err
		}
		active, err := parseChatGPTBaseLine(string(chatGPTBaseLine.Find(block)))
		return err == nil && strings.TrimRight(active, "/") == strings.TrimRight(expectedLocalURL, "/"), upstream, err
	}
	if line := chatGPTBaseLine.Find(body); line != nil {
		upstream, err := parseChatGPTBaseLine(string(line))
		return false, upstream, err
	}
	return false, defaultChatGPTBase, nil
}

func proxyBlock(body []byte) (int, int, []byte) {
	start := strings.Index(string(body), proxyMarkerBegin)
	if start < 0 {
		return -1, -1, nil
	}
	relativeEnd := strings.Index(string(body[start:]), proxyMarkerEnd)
	if relativeEnd < 0 {
		return start, -1, nil
	}
	end := start + relativeEnd + len(proxyMarkerEnd)
	if end < len(body) && body[end] == '\r' {
		end++
	}
	if end < len(body) && body[end] == '\n' {
		end++
	}
	return start, end, body[start:end]
}

func proxyBlockMetadata(block []byte) ([]byte, string, error) {
	if block == nil {
		return nil, "", errors.New("LazyMind Codex proxy configuration is incomplete")
	}
	var original, upstream string
	for _, line := range strings.Split(string(block), "\n") {
		switch {
		case strings.HasPrefix(line, proxyOriginalPrefix):
			original = strings.TrimPrefix(line, proxyOriginalPrefix)
		case strings.HasPrefix(line, proxyUpstreamPrefix):
			upstream = strings.TrimPrefix(line, proxyUpstreamPrefix)
		}
	}
	originalBytes, err := base64.RawStdEncoding.DecodeString(original)
	if err != nil {
		return nil, "", errors.New("invalid saved Codex ChatGPT base URL line")
	}
	upstreamBytes, err := base64.RawStdEncoding.DecodeString(upstream)
	if err != nil || len(upstreamBytes) == 0 {
		return nil, "", errors.New("invalid saved Codex ChatGPT upstream")
	}
	return originalBytes, string(upstreamBytes), nil
}

func buildProxyBlock(localBaseURL string, original []byte, upstream string) string {
	return proxyMarkerBegin + "\n" +
		proxyOriginalPrefix + base64.RawStdEncoding.EncodeToString(original) + "\n" +
		proxyUpstreamPrefix + base64.RawStdEncoding.EncodeToString([]byte(upstream)) + "\n" +
		"chatgpt_base_url = " + strconv.Quote(strings.TrimRight(localBaseURL, "/")) + "\n" +
		proxyMarkerEnd + "\n"
}

func parseChatGPTBaseLine(line string) (string, error) {
	parts := strings.SplitN(strings.TrimSpace(line), "=", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) != "chatgpt_base_url" {
		return "", errors.New("invalid chatgpt_base_url in Codex config")
	}
	value := strings.TrimSpace(parts[1])
	var parsed string
	var err error
	switch {
	case strings.HasPrefix(value, `"`):
		parsed, err = strconv.Unquote(value)
	case strings.HasPrefix(value, `'`) && strings.HasSuffix(value, `'`):
		parsed = strings.Trim(value, `'`)
	default:
		fields := strings.Fields(value)
		if len(fields) == 0 {
			return "", errors.New("invalid chatgpt_base_url in Codex config")
		}
		parsed = fields[0]
	}
	if err != nil || strings.TrimSpace(parsed) == "" {
		return "", errors.New("invalid chatgpt_base_url in Codex config")
	}
	return strings.TrimRight(strings.TrimSpace(parsed), "/"), nil
}

func writeConfig(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".config.toml.lazymind-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(body)
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func codexHome() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("CODEX_HOME")); configured != "" {
		return filepath.Abs(configured)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

func installationID(home string) (string, error) {
	path := filepath.Join(home, "installation_id")
	body, err := os.ReadFile(path)
	if err == nil && strings.TrimSpace(string(body)) != "" {
		return strings.TrimSpace(string(body)), nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	hexValue := hex.EncodeToString(value)
	id := fmt.Sprintf("%s-%s-%s-%s-%s", hexValue[:8], hexValue[8:12], hexValue[12:16], hexValue[16:20], hexValue[20:])
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

func accountID(home string) (string, error) {
	body, err := os.ReadFile(filepath.Join(home, "auth.json"))
	if err != nil {
		return "", fmt.Errorf("read Codex authentication: %w", err)
	}
	var auth struct {
		Tokens struct {
			AccountID string `json:"account_id"`
		} `json:"tokens"`
	}
	if json.Unmarshal(body, &auth) != nil || strings.TrimSpace(auth.Tokens.AccountID) == "" {
		return "", errors.New("Codex ChatGPT account is required for native control")
	}
	return strings.TrimSpace(auth.Tokens.AccountID), nil
}
