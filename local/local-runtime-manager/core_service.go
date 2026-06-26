package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	coreServiceHealthPath    = "/health"
	coreServiceHealthTimeout = 180 * time.Second
)

var coreServiceDBWaitTimeout = 180 * time.Second

type CoreServiceManager struct {
	runner CommandRunner
}

func NewCoreServiceManager(r CommandRunner) *CoreServiceManager {
	return &CoreServiceManager{runner: r}
}

func (m *CoreServiceManager) Run(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	for _, dir := range []string{
		filepath.Join(paths.RepoRoot, "data", "core", "uploads"),
		filepath.Join(paths.RepoRoot, "data", "subagent"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := m.buildCore(ctx, paths); err != nil {
		return err
	}
	if err := m.waitForCoreDatabase(ctx, cfg, paths); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, paths.CoreBin)
	cmd.Dir = filepath.Join(paths.RepoRoot, coreSourceDirName)
	cmd.Env = append(os.Environ(), coreServiceEnv(cfg, paths)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start core failed: %w", err)
	}
	if err := os.WriteFile(paths.CorePIDFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		_ = cmd.Process.Kill()
		return err
	}

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- cmd.Wait()
	}()

	if err := waitForCoreServiceHealth(ctx, cfg.LocalProxy.CoreHostPort, coreServiceHealthTimeout, waitErr); err != nil {
		_ = cmd.Process.Kill()
		_ = os.Remove(paths.CorePIDFile)
		return err
	}

	err := <-waitErr
	_ = os.Remove(paths.CorePIDFile)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("core exited: %w", err)
	}
	return nil
}

func (m *CoreServiceManager) buildCore(ctx context.Context, paths RuntimePaths) error {
	if err := os.MkdirAll(filepath.Dir(paths.CoreBin), 0o755); err != nil {
		return err
	}
	goBin := strings.TrimSpace(os.Getenv("GO"))
	if goBin == "" {
		goBin = "go"
	}
	res, err := m.runner.Run(ctx, Command{
		Name: goBin,
		Args: []string{"build", "-buildvcs=false", "-o", paths.CoreBin, "."},
		Dir:  filepath.Join(paths.RepoRoot, coreSourceDirName),
	})
	if err != nil {
		return fmt.Errorf("build core failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func (m *CoreServiceManager) Down(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	raw, err := os.ReadFile(paths.CorePIDFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		_ = os.Remove(paths.CorePIDFile)
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(paths.CorePIDFile)
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
			_ = os.Remove(paths.CorePIDFile)
			return nil
		case <-ticker.C:
			alive, err := upLockProcessAlive(paths.CorePIDFile)
			if err != nil || !alive {
				_ = os.Remove(paths.CorePIDFile)
				return nil
			}
		}
	}
}

func coreServiceEnv(cfg RuntimeConfig, paths RuntimePaths) []string {
	uploads := filepath.Join(paths.RepoRoot, "data", "core", "uploads")
	tempDir := filepath.Join(uploads, ".lazyllm_temp")
	imageCache := filepath.Join(uploads, ".image_cache")
	endpoints := serviceEndpointsFromConfig(cfg)
	databaseDSN := coreDatabaseDSN(cfg.Algorithm.PostgresPort, "core", "root", "123456")
	readonlyDSN := coreDatabaseDSN(cfg.Algorithm.PostgresPort, "app", "app", "app")
	return []string{
		"LAZYMIND_RUNTIME_MODE=local",
		"LAZYMIND_CORE_HOST=127.0.0.1",
		"LAZYMIND_CORE_PORT=" + strconv.Itoa(cfg.LocalProxy.CoreHostPort),
		"ACL_DB_DRIVER=postgres",
		"ACL_DB_DSN=" + databaseDSN,
		"MIGRATIONS_DIR=" + filepath.Join(paths.RepoRoot, coreSourceDirName, "migrations"),
		"LAZYMIND_REDIS_URL=",
		"LAZYMIND_STATE_BACKEND=sqlite",
		"LAZYMIND_STATE_SQLITE_DIR=" + paths.CoreStateDir,
		"LAZYMIND_UPLOAD_ROOT=" + uploads,
		"LAZYMIND_SHARED_UPLOAD_DIR=" + uploads,
		"LAZYLLM_TEMP_DIR=" + tempDir,
		"LAZYMIND_OCR_CACHE_DIR=" + imageCache,
		"LAZYMIND_UPLOAD_TEXT_UTF8_CONVERT_ENABLED=" + envText("LAZYMIND_UPLOAD_TEXT_UTF8_CONVERT_ENABLED", "true"),
		"LAZYMIND_PUBLIC_BASE_URL=http://localhost:" + strconv.Itoa(cfg.LocalProxy.Port) + "/api/core",
		"LAZYMIND_FILE_URL_SIGN_SECRET=" + envText("LAZYMIND_FILE_URL_SIGN_SECRET", "changeme-in-production"),
		"LAZYMIND_FILE_URL_EXPIRE_SECONDS=" + envText("LAZYMIND_FILE_URL_EXPIRE_SECONDS", "3600"),
		"LAZYMIND_AUTH_SERVICE_URL=" + endpoints.Host.AuthServiceBaseURL + "/api/authservice",
		"LAZYMIND_ALGO_SERVICE_URL=" + endpoints.Host.DocumentServiceBaseURL,
		"LAZYMIND_DOCUMENT_SERVICE_URL=" + endpoints.Host.DocumentServiceBaseURL,
		"LAZYMIND_PARSING_SERVICE_URL=" + endpoints.Host.ProcessorBaseURL,
		"LAZYMIND_PROCESSOR_SERVICE_URL=" + endpoints.Host.ProcessorBaseURL,
		"LAZYMIND_CHAT_SERVICE_URL=" + endpoints.Host.ChatBaseURL,
		"LAZYMIND_EVO_SERVICE_URL=" + endpoints.Host.EvoBaseURL,
		"LAZYMIND_CORE_SELF_URL=" + endpoints.Host.CoreBaseURL,
		"LAZYMIND_SCAN_CONTROL_PLANE_URL=http://127.0.0.1:" + strconv.Itoa(cfg.LocalProxy.ScanHostPort),
		"LAZYMIND_OFFICE_CONVERT_URL=" + endpoints.Host.OfficeConvertURL,
		"LAZYMIND_OFFICE_CONVERT_WORKERS=" + envText("LAZYMIND_OFFICE_CONVERT_WORKERS", "4"),
		"LAZYMIND_SUBAGENT_WORKSPACE=" + filepath.Join(paths.RepoRoot, "data", "subagent"),
		"LAZYMIND_SUBAGENT_DB_DSN=" + databaseDSN,
		"LAZYMIND_READONLY_VALIDATE=0",
		"LAZYMIND_READONLY_DB_DRIVER=postgres",
		"LAZYMIND_READONLY_DB_DSN=" + readonlyDSN,
		"LAZYMIND_READONLY_SCHEMA=public",
		"LAZYMIND_READONLY_TABLES=public.lazyllm_documents,public.lazyllm_doc_service_tasks,public.lazyllm_kb_documents",
		"LAZYMIND_RESOURCE_UPDATE_ENABLED=" + envText("LAZYMIND_RESOURCE_UPDATE_ENABLED", "true"),
		"LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN=" + envText("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "dev-internal-service-token"),
	}
}

func (m *CoreServiceManager) waitForCoreDatabase(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	deadline := time.NewTimer(coreServiceDBWaitTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		res, err := m.runner.Run(ctx, Command{
			Name: "docker",
			Args: []string{
				"compose",
				"-f", repoComposeFileName,
				"-f", localComposeOverrideName,
				"exec",
				"-T",
				"db",
				"pg_isready",
				"-U", "root",
				"-d", "core",
			},
			Dir: paths.RepoRoot,
		})
		if err == nil && strings.Contains(res.Stdout+res.Stderr, "accepting connections") {
			return nil
		}
		if err != nil {
			lastErr = err
		} else if stderr := strings.TrimSpace(res.Stderr); stderr != "" {
			lastErr = fmt.Errorf("%s", stderr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if lastErr != nil {
				return fmt.Errorf("core database did not become ready: %w", lastErr)
			}
			return fmt.Errorf("core database did not become ready at %s", serviceEndpointsFromConfig(cfg).Host.PostgresAddress)
		case <-ticker.C:
		}
	}
}

func waitForCoreServiceHealth(ctx context.Context, port int, timeout time.Duration, waitErr <-chan error) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if coreServiceHealthAlive(port, time.Second) {
			return nil
		}
		select {
		case err := <-waitErr:
			if err == nil {
				return fmt.Errorf("core exited before becoming healthy")
			}
			return fmt.Errorf("core exited before becoming healthy: %w", err)
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("core health check timed out on port %d", port)
		case <-ticker.C:
		}
	}
}

func coreServiceHealthAlive(port int, timeout time.Duration) bool {
	client := http.Client{Timeout: timeout}
	resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + coreServiceHealthPath)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
