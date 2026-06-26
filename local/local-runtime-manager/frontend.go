package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type FrontendManager struct {
	runner CommandRunner
}

func NewFrontendManager(r CommandRunner) *FrontendManager {
	return &FrontendManager{runner: r}
}

func (m *FrontendManager) Run(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(paths.CaddyBin), 0o755); err != nil {
		return err
	}

	frontendDir := filepath.Join(paths.RepoRoot, "frontend")
	install := Command{Name: "pnpm", Args: []string{"install", "--frozen-lockfile"}, Dir: frontendDir}
	if res, err := m.runner.Run(ctx, install); err != nil {
		return fmt.Errorf("frontend dependency install failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}

	build := Command{
		Name: "pnpm",
		Args: []string{"build"},
		Dir:  frontendDir,
		Env:  frontendBuildEnv(),
	}
	if res, err := m.runner.Run(ctx, build); err != nil {
		return fmt.Errorf("frontend build failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}

	if err := writeCaddyfile(paths, cfg); err != nil {
		return err
	}
	caddyBin, err := m.ensureCaddy(ctx, cfg, paths)
	if err != nil {
		return err
	}

	run := Command{
		Name: caddyBin,
		Args: []string{"run", "--config", paths.CaddyConfig, "--adapter", "caddyfile"},
		Dir:  paths.RepoRoot,
	}
	if res, err := m.runner.Run(ctx, run); err != nil {
		return fmt.Errorf("frontend caddy exited: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func (m *FrontendManager) Down(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	script := `
port="$1"
config="$2"
if command -v lsof >/dev/null 2>&1; then
  for pid in $(lsof -t -nP -iTCP:"$port" -sTCP:LISTEN 2>/dev/null | sort -u); do
    cmd="$(ps -p "$pid" -o command= 2>/dev/null || true)"
    case "$cmd" in
      *caddy*"$config"*|*caddy*)
        echo "Stopping frontend Caddy on :$port ($pid)..."
        kill "$pid" 2>/dev/null || true
        ;;
    esac
  done
fi
exit 0
`
	res, err := m.runner.Run(ctx, Command{
		Name: "sh",
		Args: []string{"-c", script, "frontend-down", strconv.Itoa(cfg.FrontendPort), paths.CaddyConfig},
		Dir:  paths.RepoRoot,
	})
	if err != nil {
		return fmt.Errorf("stop frontend failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func frontendBuildEnv() []string {
	mode := strings.TrimSpace(os.Getenv("VITE_LAZYMIND_MODE"))
	if mode == "" {
		mode = "local"
	}
	env := []string{"VITE_LAZYMIND_MODE=" + mode}
	for _, key := range []string{"VITE_HIDE_EVO", "VITE_API_BASE_URL", "VITE_APP_LOGO", "VITE_APP_CHAT_TITLE"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func writeCaddyfile(paths RuntimePaths, cfg RuntimeConfig) error {
	distRoot := filepath.ToSlash(filepath.Join(paths.RepoRoot, "frontend", "dist"))
	proxy := "http://127.0.0.1:" + strconv.Itoa(cfg.LocalProxy.Port)
	content := fmt.Sprintf(`{
	admin off
	auto_https off
}

http://localhost:%d, http://127.0.0.1:%d {
	bind 127.0.0.1
	root * %s
	encode gzip

	handle /api/* {
		reverse_proxy %s {
			flush_interval -1
		}
	}

	handle /api-docs/* {
		reverse_proxy %s {
			flush_interval -1
		}
	}

	handle {
		try_files {path} /index.html
		file_server
	}
}
`, cfg.FrontendPort, cfg.FrontendPort, strconv.Quote(distRoot), proxy, proxy)
	return os.WriteFile(paths.CaddyConfig, []byte(content), 0o644)
}

func (m *FrontendManager) ensureCaddy(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) (string, error) {
	if explicit := strings.TrimSpace(os.Getenv(caddyBinEnvVar)); explicit != "" {
		return explicit, nil
	}
	if info, err := os.Stat(paths.CaddyBin); err == nil && !info.IsDir() {
		return paths.CaddyBin, nil
	}
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("automatic Caddy download is only supported on linux in this local profile; set %s", caddyBinEnvVar)
	}
	switch runtime.GOARCH {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("automatic Caddy download does not support %s/%s; set %s", runtime.GOOS, runtime.GOARCH, caddyBinEnvVar)
	}
	if err := downloadCaddy(ctx, cfg.CaddyVersion, runtime.GOOS, runtime.GOARCH, paths.CaddyBin); err != nil {
		return "", err
	}
	return paths.CaddyBin, nil
}

func caddyArchiveURL(version, goos, goarch string) string {
	return fmt.Sprintf("https://github.com/caddyserver/caddy/releases/download/v%s/caddy_%s_%s_%s.tar.gz", version, version, goos, goarch)
}

func downloadCaddy(ctx context.Context, version, goos, goarch, dest string) error {
	url := caddyArchiveURL(version, goos, goarch)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download Caddy %s failed: %w", version, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download Caddy %s failed: HTTP %d from %s", version, resp.StatusCode, url)
	}

	gzr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("read Caddy archive: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	tmp := dest + ".tmp"
	defer os.Remove(tmp)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("extract Caddy archive: %w", err)
		}
		if filepath.Base(hdr.Name) != "caddy" || hdr.FileInfo().IsDir() {
			continue
		}
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			_ = out.Close()
			return fmt.Errorf("write Caddy binary: %w", err)
		}
		if err := out.Close(); err != nil {
			return err
		}
		if err := os.Rename(tmp, dest); err != nil {
			return err
		}
		return os.Chmod(dest, 0o755)
	}
	return fmt.Errorf("Caddy archive did not contain caddy binary")
}
