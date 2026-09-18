package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"github.com/google/uuid"
	"lazymind/core/common/secretcrypto"
)

type recoveryOptions struct {
	AcceptanceRoot  string
	LocalConfigPath string
	SourceKey       string
	Apply           bool
}

type recoveryReport struct {
	Reencrypted    int    `json:"reencrypted"`
	AlreadyCurrent int    `json:"already_current"`
	BackupDir      string `json:"backup_dir,omitempty"`
}

func recoverCredentials(o recoveryOptions) (report recoveryReport, resultErr error) {
	defer func() {
		if resultErr != nil {
			report.Reencrypted = 0
		}
	}()
	if strings.TrimSpace(o.SourceKey) == "" || len(o.SourceKey) > 4096 || o.AcceptanceRoot == "" || o.LocalConfigPath == "" {
		return report, errors.New("recovery requires explicit paths and a source key")
	}
	root, err := filepath.Abs(o.AcceptanceRoot)
	if err != nil || checkPath(root, false) != nil || checkPath(o.LocalConfigPath, false) != nil {
		return report, errors.New("unsafe recovery path")
	}
	identityPath := filepath.Join(root, "profile", "credential-device.json")
	dbPath := filepath.Join(root, "runtime", "data", "stores", "sqlite", "core", "core.db")
	if checkPath(identityPath, false) != nil || checkPath(dbPath, false) != nil {
		return report, errors.New("missing or unsafe identity/database")
	}
	identityRaw, err := readSmallFile(identityPath)
	if err != nil {
		return report, errors.New("cannot read desktop identity")
	}
	target, err := desktopKey(identityRaw)
	if err != nil {
		return report, err
	}
	config, err := readSmallFile(o.LocalConfigPath)
	if err != nil {
		return report, errors.New("cannot read Local configuration")
	}
	nextConfig, err := localConfig(config, root, o.SourceKey, target)
	if err != nil {
		return report, err
	}
	// Share RuntimeManager.acquireUpLock's O_EXCL/PID protocol; it prevents startup
	// and a second recovery while this offline operation owns the runtime.
	lockPath := filepath.Join(root, "runtime", "run", "up.lock")
	if checkPath(filepath.Dir(lockPath), false) != nil {
		return report, errors.New("unsafe runtime lock directory")
	}
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return report, errors.New("runtime startup or maintenance lock is present")
	}
	defer os.Remove(lockPath)
	_, writeErr := fmt.Fprintln(lock, os.Getpid())
	closeErr := lock.Close()
	if writeErr != nil || closeErr != nil {
		return report, errors.New("cannot acquire runtime maintenance lock")
	}
	if err := requireStopped(root); err != nil {
		return report, err
	}
	mode := "ro"
	if o.Apply {
		mode = "rw"
	}
	dsn := (&url.URL{Scheme: "file", Path: dbPath, RawQuery: "mode=" + mode + "&_pragma=busy_timeout(3000)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return report, errors.New("cannot open recovery database")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	changes, current, err := inspectCredentials(ctx, db, o.SourceKey, target)
	if err != nil {
		return report, err
	}
	report.Reencrypted, report.AlreadyCurrent = len(changes), current
	if !o.Apply || (len(changes) == 0 && bytes.Equal(config, nextConfig)) {
		return report, nil
	}
	configDir := filepath.Dir(o.LocalConfigPath)
	if err := writableDirectory(configDir); err != nil {
		return report, err
	}
	backupParent := filepath.Join(root, "credential-recovery-backups")
	if err := checkPath(backupParent, true); err != nil {
		return report, errors.New("unsafe backup directory")
	}
	if err := os.MkdirAll(backupParent, 0700); err != nil {
		return report, errors.New("cannot create backup directory")
	}
	if err := writableDirectory(backupParent); err != nil {
		return report, err
	}
	backup, err := os.MkdirTemp(backupParent, "recovery-")
	if err != nil {
		return report, errors.New("cannot create protected backup")
	}
	report.BackupDir = backup
	for name, raw := range map[string][]byte{"config.env": config, "credential-device.json": identityRaw, "core.db": {}} {
		if err := os.WriteFile(filepath.Join(backup, name), raw, 0600); err != nil {
			return report, errors.New("cannot write protected backup")
		}
	}
	// VACUUM INTO copies a consistent SQLite snapshot, including committed WAL.
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", filepath.Join(backup, "core.db")); err != nil {
		return report, errors.New("cannot snapshot recovery database")
	}
	for _, name := range []string{"core.db", "config.env", "credential-device.json"} {
		if err := syncFile(filepath.Join(backup, name)); err != nil {
			return report, errors.New("cannot persist protected backup")
		}
	}
	if err := syncDirectory(backup); err != nil {
		return report, errors.New("cannot persist backup directory")
	}
	if err := syncDirectory(backupParent); err != nil {
		return report, errors.New("cannot persist backup parent directory")
	}
	if err := requireStopped(root); err != nil {
		return report, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return report, errors.New("cannot begin recovery transaction")
	}
	defer tx.Rollback()
	for _, change := range changes {
		result, err := tx.ExecContext(ctx, `UPDATE user_model_provider_groups SET api_key_ciphertext=?
 WHERE id=? AND api_key_ciphertext=? AND api_key=''`, change.next, change.id, change.previous)
		if err != nil {
			return report, errors.New("credential update failed; transaction rolled back")
		}
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			return report, errors.New("credential changed during recovery; transaction rolled back")
		}
	}
	latest, err := readSmallFile(o.LocalConfigPath)
	if err != nil || !bytes.Equal(config, latest) {
		return report, errors.New("Local configuration changed during recovery")
	}
	if replaced, err := replacePrivateFile(o.LocalConfigPath, nextConfig); err != nil {
		if replaced {
			if _, restoreErr := replacePrivateFile(o.LocalConfigPath, config); restoreErr != nil {
				return report, errors.New("config restore failed; restore protected backup before startup")
			}
		}
		return report, errors.New("cannot replace Local configuration; transaction rolled back")
	}
	// Config and SQLite cannot commit atomically. An interrupted run accepts either
	// source or target ciphertext on the next explicit recovery. Backups stay intact.
	if err := tx.Commit(); err != nil {
		if _, restoreErr := replacePrivateFile(o.LocalConfigPath, config); restoreErr != nil {
			return report, errors.New("commit/config restore failed; restore protected backup before startup")
		}
		return report, errors.New("credential commit failed; original Local configuration restored")
	}
	return report, nil
}

type credentialChange struct{ id, previous, next string }

func inspectCredentials(ctx context.Context, db *sql.DB, source, target string) ([]credentialChange, int, error) {
	rows, err := db.QueryContext(ctx, "SELECT id, api_key, api_key_ciphertext FROM user_model_provider_groups ORDER BY id")
	if err != nil {
		return nil, 0, errors.New("cannot inspect model credentials")
	}
	defer rows.Close()
	var changes []credentialChange
	current := 0
	for rows.Next() {
		var id, plaintext, raw string
		if rows.Scan(&id, &plaintext, &raw) != nil || plaintext != "" {
			return nil, 0, errors.New("unsupported credential record; no changes made")
		}
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var wrapper secretcrypto.Wrapper
		if json.Unmarshal([]byte(raw), &wrapper) != nil || wrapper.Enc != "aes-gcm" || len(raw) > 1024*1024 {
			return nil, 0, errors.New("invalid encrypted credential")
		}
		nonce, err := base64.StdEncoding.DecodeString(wrapper.Nonce)
		if err != nil || len(nonce) != 12 {
			return nil, 0, errors.New("invalid encrypted credential nonce")
		}
		if value, ok, err := secretcrypto.DecodeAESGCM(json.RawMessage(raw), target); err == nil && ok {
			clear(value)
			current++
			continue
		}
		value, ok, err := secretcrypto.DecodeAESGCM(json.RawMessage(raw), source)
		if err != nil || !ok {
			return nil, 0, errors.New("credential does not match the explicit source or desktop key")
		}
		next, err := secretcrypto.EncodeAESGCM(value, target)
		clear(value)
		if err != nil {
			return nil, 0, errors.New("credential encryption failed")
		}
		changes = append(changes, credentialChange{id: id, previous: raw, next: string(next)})
	}
	if rows.Err() != nil {
		return nil, 0, errors.New("credential inspection failed")
	}
	return changes, current, nil
}

func desktopKey(raw []byte) (string, error) {
	var identity struct {
		Version      int    `json:"version"`
		DeviceID     string `json:"deviceId"`
		DeviceSecret string `json:"deviceSecret"`
	}
	if json.Unmarshal(raw, &identity) != nil || identity.Version != 1 {
		return "", errors.New("invalid desktop identity")
	}
	if _, err := uuid.Parse(identity.DeviceID); err != nil {
		return "", errors.New("invalid desktop device ID")
	}
	secret, err := base64.RawURLEncoding.DecodeString(identity.DeviceSecret)
	if err != nil || len(secret) != 32 {
		return "", errors.New("invalid desktop device secret")
	}
	defer clear(secret)
	// Same purpose separation as desktop/electron/src/main.js.
	h := hmac.New(sha256.New, secret)
	_, _ = h.Write([]byte("lazymind/desktop/" + identity.DeviceID + "/model-provider/v1"))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil)), nil
}

func localConfig(raw []byte, root, source, target string) ([]byte, error) {
	const name = "LAZYMIND_MODEL_PROVIDER_SECRET_KEY"
	lines := strings.Split(string(raw), "\n")
	found, rootMatches := false, false
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		if strings.HasPrefix(line, "LAZYMIND_RUNTIME_ROOT=") {
			rootMatches = strings.TrimSpace(strings.TrimPrefix(line, "LAZYMIND_RUNTIME_ROOT=")) == filepath.Join(root, "runtime")
		}
		if !strings.Contains(line, name) {
			continue
		}
		if found || !strings.HasPrefix(line, name+"=") {
			return nil, errors.New("ambiguous Local model key configuration")
		}
		found = true
		value := strings.TrimSpace(strings.TrimPrefix(line, name+"="))
		if value != strings.TrimSpace(source) && value != target {
			return nil, errors.New("Local configuration uses another model key")
		}
		lines[i] = name + "=" + target
	}
	if !rootMatches {
		return nil, errors.New("Local configuration points to another runtime")
	}
	result := strings.Join(lines, "\n")
	if !found {
		if !strings.HasSuffix(result, "\n") {
			result += "\n"
		}
		result += name + "=" + target + "\n"
	}
	return []byte(result), nil
}

func requireStopped(root string) error {
	path := filepath.Join(root, "runtime", "state", "runtime-state.json")
	raw, err := readSmallFile(path)
	var state struct {
		OverallStatus string `json:"overallStatus"`
		Services      map[string]struct {
			Status string `json:"status"`
		} `json:"services"`
	}
	if err != nil || json.Unmarshal(raw, &state) != nil || state.OverallStatus != "stopped" {
		return errors.New("stop the acceptance runtime before recovery")
	}
	for _, service := range state.Services {
		if service.Status != "stopped" {
			return errors.New("runtime service is not stopped")
		}
	}
	registryPath := filepath.Join(root, "runtime", "run", "processes.json")
	if _, err := os.Lstat(registryPath); err == nil {
		var registry struct {
			Processes []struct {
				PID int `json:"pid"`
			} `json:"processes"`
		}
		raw, err := readSmallFile(registryPath)
		if err != nil || json.Unmarshal(raw, &registry) != nil {
			return errors.New("cannot verify runtime processes")
		}
		for _, process := range registry.Processes {
			if process.PID <= 0 || !processStopped(process.PID) {
				return errors.New("runtime process is still active or unverifiable")
			}
		}
	} else if !os.IsNotExist(err) {
		return errors.New("cannot inspect runtime process registry")
	}
	appPID := filepath.Join(root, "app.pid")
	if _, err := os.Lstat(appPID); err == nil {
		raw, err := readSmallFile(appPID)
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil || parseErr != nil || pid <= 0 || !processStopped(pid) {
			return errors.New("desktop process is still active or unverifiable")
		}
	} else if !os.IsNotExist(err) {
		return errors.New("cannot inspect desktop process")
	}
	return nil
}

func processStopped(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	defer process.Release()
	err = process.Signal(syscall.Signal(0))
	return errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH)
}

// Lstat each component, allowing only macOS's system /var and /tmp aliases.
func checkPath(path string, allowMissing bool) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for p := abs; p != filepath.Dir(p); p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if os.IsNotExist(err) && allowMissing && p == abs {
			continue
		}
		if err != nil {
			return errors.New("path is unavailable")
		}
		if info.Mode()&os.ModeSymlink != 0 && p != "/var" && p != "/tmp" {
			return errors.New("symbolic link is not allowed")
		}
	}
	return nil
}

func readSmallFile(path string) ([]byte, error) {
	if err := checkPath(path, false); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4*1024*1024 {
		return nil, errors.New("invalid recovery file")
	}
	return io.ReadAll(io.LimitReader(f, 4*1024*1024+1))
}

func writableDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0200 == 0 {
		return errors.New("recovery directory is not writable")
	}
	return nil
}

func replacePrivateFile(path string, raw []byte) (bool, error) {
	if err := checkPath(path, false); err != nil {
		return false, err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".credential-config-")
	if err != nil {
		return false, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(raw); err != nil {
		return false, err
	}
	if err := f.Sync(); err != nil {
		return false, err
	}
	if err := f.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return false, err
	}
	return true, syncDirectory(filepath.Dir(path))
}

func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func run(args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("acceptance-model-credentials", flag.ContinueOnError)
	flags.SetOutput(io.Discard) // Flag errors may echo user-supplied secrets.
	var o recoveryOptions
	flags.StringVar(&o.AcceptanceRoot, "acceptance-root", "", "explicit acceptance directory")
	flags.StringVar(&o.LocalConfigPath, "local-config", "", "ignored Local configuration")
	flags.BoolVar(&o.Apply, "apply", false, "apply after protected backup")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprintln(errOut, "usage: acceptance-model-credentials --acceptance-root DIR --local-config FILE [--apply]; source key from LAZYMIND_RECOVERY_SOURCE_KEY")
		return 2
	}
	o.SourceKey = os.Getenv("LAZYMIND_RECOVERY_SOURCE_KEY")
	report, err := recoverCredentials(o)
	if err != nil {
		fmt.Fprintln(errOut, err.Error())
		if report.BackupDir != "" {
			_ = json.NewEncoder(out).Encode(report)
		}
		return 1
	}
	if json.NewEncoder(out).Encode(report) != nil {
		return 1
	}
	return 0
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
