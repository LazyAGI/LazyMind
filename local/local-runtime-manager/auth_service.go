package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	authServiceOpenAPIExportEnvVar = "LAZYMIND_AUTH_OPENAPI_EXPORT_ENABLED"
	authServiceHealthPath          = "/api/authservice/auth/health"
	authServiceHealthTimeout       = 180 * time.Second
	authServiceDBWaitTimeout       = 180 * time.Second
)

type AuthServiceManager struct {
	runner CommandRunner
}

func NewAuthServiceManager(r CommandRunner) *AuthServiceManager {
	return &AuthServiceManager{runner: r}
}

func (m *AuthServiceManager) Run(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	if err := m.preparePythonEnv(ctx, cfg, paths); err != nil {
		return err
	}
	if err := waitForAuthDatabase(ctx, cfg.AuthService.DatabaseURL); err != nil {
		return err
	}

	python := authServicePythonPath(paths)
	cmd := exec.CommandContext(
		ctx,
		python,
		"-m",
		"uvicorn",
		"main:app",
		"--host",
		"127.0.0.1",
		"--port",
		strconv.Itoa(cfg.AuthService.Port),
	)
	cmd.Dir = filepath.Join(paths.RepoRoot, authServiceSourceDirName)
	cmd.Env = append(os.Environ(), authServiceEnv(cfg, paths)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start auth-service failed: %w", err)
	}
	if err := os.WriteFile(paths.AuthServicePIDFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		_ = cmd.Process.Kill()
		return err
	}

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- cmd.Wait()
	}()

	if err := waitForAuthServiceHealth(ctx, cfg.AuthService.Port, authServiceHealthTimeout, waitErr); err != nil {
		_ = cmd.Process.Kill()
		_ = os.Remove(paths.AuthServicePIDFile)
		return err
	}

	err := <-waitErr
	_ = os.Remove(paths.AuthServicePIDFile)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("auth-service exited: %w", err)
	}
	return nil
}

func (m *AuthServiceManager) Down(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	_ = cfg
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	raw, err := os.ReadFile(paths.AuthServicePIDFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		_ = os.Remove(paths.AuthServicePIDFile)
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(paths.AuthServicePIDFile)
		return nil
	}
	if err := proc.Signal(os.Interrupt); err != nil {
		_ = proc.Kill()
	}

	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = proc.Kill()
			return ctx.Err()
		case <-deadline.C:
			_ = proc.Kill()
			_ = os.Remove(paths.AuthServicePIDFile)
			return nil
		case <-ticker.C:
			if !authServiceHealthAlive(cfg.AuthService.Port, 250*time.Millisecond) {
				_ = os.Remove(paths.AuthServicePIDFile)
				return nil
			}
		}
	}
}

func (m *AuthServiceManager) preparePythonEnv(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	python := authServicePythonPath(paths)
	if _, err := os.Stat(python); err != nil {
		if err := m.createPythonEnv(ctx, cfg, paths); err != nil {
			return err
		}
	}

	if !cfg.AuthService.InstallDeps {
		return nil
	}
	requirements := filepath.Join(paths.RepoRoot, authServiceSourceDirName, "requirements.txt")
	hash, err := fileSHA256(requirements)
	if err != nil {
		return err
	}
	marker := filepath.Join(paths.AuthServiceVenvDir, ".lazymind-requirements.sha256")
	if b, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(b)) == hash {
		return nil
	}

	if err := m.installRequirements(ctx, paths, python, requirements); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte(hash+"\n"), 0o644)
}

func (m *AuthServiceManager) createPythonEnv(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	uv := envText(authServiceUVEnvVar, "uv")
	res, runErr := m.runner.Run(ctx, Command{
		Name: uv,
		Args: []string{"venv", "--python", cfg.AuthService.Python, paths.AuthServiceVenvDir},
		Dir:  paths.RepoRoot,
	})
	if runErr == nil {
		return nil
	}
	uvErr := fmt.Sprintf("%v (%s)", runErr, strings.TrimSpace(res.Stderr))

	res, runErr = m.runner.Run(ctx, Command{
		Name: cfg.AuthService.Python,
		Args: []string{"-m", "venv", paths.AuthServiceVenvDir},
		Dir:  paths.RepoRoot,
	})
	if runErr != nil {
		return fmt.Errorf("create auth-service venv failed: uv: %s; python venv: %w (%s)", uvErr, runErr, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func (m *AuthServiceManager) installRequirements(ctx context.Context, paths RuntimePaths, python string, requirements string) error {
	uv := envText(authServiceUVEnvVar, "uv")
	res, runErr := m.runner.Run(ctx, Command{
		Name: uv,
		Args: []string{"pip", "install", "--python", python, "-r", requirements},
		Dir:  paths.RepoRoot,
	})
	if runErr == nil {
		return nil
	}
	uvErr := fmt.Sprintf("%v (%s)", runErr, strings.TrimSpace(res.Stderr))

	res, runErr = m.runner.Run(ctx, Command{
		Name: python,
		Args: []string{"-m", "pip", "install", "-r", requirements},
		Dir:  paths.RepoRoot,
	})
	if runErr != nil {
		return fmt.Errorf("install auth-service requirements failed: uv: %s; pip: %w (%s)", uvErr, runErr, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func authServicePythonPath(paths RuntimePaths) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(paths.AuthServiceVenvDir, "Scripts", "python.exe")
	}
	return filepath.Join(paths.AuthServiceVenvDir, "bin", "python")
}

func authServiceEnv(cfg RuntimeConfig, paths RuntimePaths) []string {
	return []string{
		"LAZYMIND_RUNTIME_MODE=local",
		"LAZYMIND_DATABASE_URL=" + cfg.AuthService.DatabaseURL,
		"LAZYMIND_REDIS_URL=",
		"LAZYMIND_STATE_BACKEND=sqlite",
		"LAZYMIND_STATE_SQLITE_DIR=" + paths.AuthServiceStateDir,
		"LAZYMIND_JWT_SECRET=" + envText("LAZYMIND_JWT_SECRET", "dev-secret-change-me"),
		"LAZYMIND_JWT_TTL_MINUTES=" + envText("LAZYMIND_JWT_TTL_MINUTES", "60"),
		"LAZYMIND_JWT_REFRESH_TTL_DAYS=" + envText("LAZYMIND_JWT_REFRESH_TTL_DAYS", "7"),
		"LAZYMIND_AUTH_CLOUD_SECRET_KEY=" + envText("LAZYMIND_AUTH_CLOUD_SECRET_KEY", "dev-ragscan-secret-key-change-me"),
		"LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN=" + envText("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "dev-internal-service-token"),
		"LAZYMIND_BOOTSTRAP_ADMIN_USERNAME=" + envText("LAZYMIND_BOOTSTRAP_ADMIN_USERNAME", "admin"),
		"LAZYMIND_BOOTSTRAP_ADMIN_PASSWORD=" + envText("LAZYMIND_BOOTSTRAP_ADMIN_PASSWORD", "admin"),
		"LAZYMIND_MODEL_CONFIG_PATH=" + envText("LAZYMIND_MODEL_CONFIG_PATH", "dynamic"),
		"LAZYMIND_CHAT_UNLIKE_SWITCH=" + envText("LAZYMIND_CHAT_UNLIKE_SWITCH", "true"),
		authServiceOpenAPIExportEnvVar + "=0",
	}
}

func waitForAuthDatabase(ctx context.Context, databaseURL string) error {
	address, ok := databaseAddress(databaseURL)
	if !ok {
		return nil
	}
	deadline := time.NewTimer(authServiceDBWaitTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := net.DialTimeout("tcp", address, time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("auth-service database did not become reachable at %s", address)
		case <-ticker.C:
		}
	}
}

func databaseAddress(databaseURL string) (string, bool) {
	u, err := url.Parse(databaseURL)
	if err != nil {
		return "", false
	}
	if !strings.HasPrefix(u.Scheme, "postgresql") {
		return "", false
	}
	host := u.Hostname()
	port := u.Port()
	if host == "" {
		return "", false
	}
	if port == "" {
		port = "5432"
	}
	return net.JoinHostPort(host, port), true
}

func waitForAuthServiceHealth(ctx context.Context, port int, timeout time.Duration, waitErr <-chan error) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if authServiceHealthAlive(port, time.Second) {
			return nil
		}
		select {
		case err := <-waitErr:
			if err == nil {
				return fmt.Errorf("auth-service exited before becoming healthy")
			}
			return fmt.Errorf("auth-service exited before becoming healthy: %w", err)
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("auth-service health check timed out on port %d", port)
		case <-ticker.C:
		}
	}
}

func authServiceHealthAlive(port int, timeout time.Duration) bool {
	client := http.Client{Timeout: timeout}
	resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + authServiceHealthPath)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func fileSHA256(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
