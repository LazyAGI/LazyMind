package codexruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"lazymind/agentconnector/internal/localfile"
)

type runtimeConfig struct {
	DesktopExecutable string                    `json:"desktopExecutable"`
	CLIVersion        string                    `json:"cliVersion"`
	Version           int                       `json:"version"`
	RuntimeDir        string                    `json:"runtimeDir"`
	BundleDir         string                    `json:"bundleDir"`
	CLI               string                    `json:"cli"`
	Node              string                    `json:"node"`
	Helper            string                    `json:"helper"`
	CodexHome         string                    `json:"codexHome"`
	Endpoint          string                    `json:"endpoint"`
	TokenFile         string                    `json:"tokenFile"`
	Plugins           map[string]map[string]any `json:"plugins"`
}
type backendRecord struct {
	Process  *ProcessInfo `json:"process"`
	Socket   any          `json:"socket,omitempty"`
	Endpoint string       `json:"endpoint"`
}

func readRuntime(file string) (*runtimeConfig, error) {
	if !filepath.IsAbs(file) {
		return nil, errors.New("absolute runtime config required")
	}
	if err := checkPrivate(file); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var c runtimeConfig
	if err = json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Version != 1 && c.Version != 2 {
		return nil, errors.New("unsupported runtime version")
	}
	for _, p := range []string{c.RuntimeDir, c.BundleDir, c.CLI, c.Node, c.CodexHome} {
		if !filepath.IsAbs(p) {
			return nil, errors.New("absolute runtime paths required")
		}
	}
	if c.Version == 2 && !filepath.IsAbs(c.Helper) {
		return nil, errors.New("native helper path required")
	}
	if err = checkPrivate(c.RuntimeDir); err != nil {
		return nil, err
	}
	if c.Endpoint == "" {
		c.Endpoint = "ws+unix://localhost" + filepath.Join(c.RuntimeDir, "daemon.sock") + ":/rpc"
	}
	if err = validateEndpoint(c.Endpoint, c.TokenFile); err != nil {
		return nil, err
	}
	if c.TokenFile != "" {
		if err = checkPrivate(c.TokenFile); err != nil {
			return nil, err
		}
	}
	return &c, nil
}
func writePrivateJSON(file string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(file), ".record-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = makePrivate(tmp); err != nil {
		return err
	}
	return localfile.Replace(tmp, file)
}
func sameProcess(want *ProcessInfo) bool {
	if want == nil {
		return false
	}
	got, err := platformProcess(want.PID)
	return err == nil && got != nil && got.Identity == want.Identity
}
func tomlValue(v any) (string, error) {
	switch value := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(value))
		for k := range value {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := []string{}
		for _, k := range keys {
			if value[k] == nil {
				continue
			}
			s, err := tomlValue(value[k])
			if err != nil {
				return "", err
			}
			parts = append(parts, strconv.Quote(k)+"="+s)
		}
		return "{" + strings.Join(parts, ",") + "}", nil
	case []any:
		parts := []string{}
		for _, item := range value {
			s, err := tomlValue(item)
			if err != nil {
				return "", err
			}
			parts = append(parts, s)
		}
		return "[" + strings.Join(parts, ",") + "]", nil
	case string:
		return strconv.Quote(value), nil
	case bool, float64:
		intent, err := json.Marshal(value)
		return string(intent), err
	default:
		return "", fmt.Errorf("unsupported TOML value %T", v)
	}
}
func backendArgs(c *runtimeConfig, file string) ([]string, error) {
	listen := "unix://" + filepath.Join(c.RuntimeDir, "daemon.sock")
	if strings.HasPrefix(c.Endpoint, "ws://") {
		listen = c.Endpoint
	}
	args := []string{"-c", "features.code_mode_host=true", "app-server", "--listen", listen}
	if c.TokenFile != "" {
		args = append(args, "--ws-auth", "capability-token", "--ws-token-file", c.TokenFile)
	}
	for _, name := range []string{"codex-app-tools@openai-bundled", "unified-computer-use@openai-bundled"} {
		args = append(args, "-c", "plugins."+strconv.Quote(name)+".enabled=false")
	}
	for _, kind := range []string{"codex_app", "cua_repl"} {
		original, ok := c.Plugins[kind]
		if !ok {
			return nil, errors.New("missing original tool configuration")
		}
		plugin := map[string]any{}
		for k, v := range original {
			plugin[k] = v
		}
		plugin["command"] = c.Node
		plugin["args"] = []any{filepath.Join(c.BundleDir, "mcp-proxy.mjs"), kind}
		plugin["cwd"] = c.RuntimeDir
		plugin["env"] = map[string]any{"LAZYMIND_CODEX_DESKTOP_CONFIG": file}
		if c.Helper != "" {
			plugin["env"].(map[string]any)["LAZYMIND_RUNTIME_HELPER"] = c.Helper
		}
		plugin["env_vars"] = []any{}
		plugin["enabled"] = true
		value, err := tomlValue(plugin)
		if err != nil {
			return nil, err
		}
		args = append(args, "-c", "mcp_servers."+kind+"="+value)
	}
	return args, nil
}
func health(ctx context.Context, c *runtimeConfig, file string) error {
	timeout, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(timeout, c.Node, filepath.Join(c.BundleDir, "health.mjs"))
	cmd.Env = append(os.Environ(), "LAZYMIND_CODEX_DESKTOP_CONFIG="+file)
	if c.Helper != "" {
		cmd.Env = append(cmd.Env, "LAZYMIND_RUNTIME_HELPER="+c.Helper)
	}
	if err := cmd.Run(); err != nil {
		return errors.New("backend health/profile/version check failed")
	}
	return nil
}
func backendLog(c *runtimeConfig, event string, pid int) {
	f, err := os.OpenFile(filepath.Join(c.RuntimeDir, "coordinator.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err == nil {
		defer f.Close()
		_ = json.NewEncoder(f).Encode(map[string]any{"event": event, "pid": pid, "time": time.Now().UTC()})
	}
}
func ensureBackend(ctx context.Context, file string) (*backendRecord, error) {
	c, err := readRuntime(file)
	if err != nil {
		return nil, err
	}
	// The caller imposes a bounded deadline. OS releases this lock on process exit.
	unlock, err := localfile.Lock(filepath.Join(c.RuntimeDir, "startup.lock"))
	if err != nil {
		return nil, err
	}
	defer unlock()
	recordPath := filepath.Join(c.RuntimeDir, "backend.json")
	var old backendRecord
	if raw, e := os.ReadFile(recordPath); e == nil {
		if err = checkPrivate(recordPath); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &old); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	if sameProcess(old.Process) {
		if old.Endpoint != c.Endpoint {
			return nil, errors.New("backend endpoint changed")
		}
		if err = verifyListener(c, &old); err != nil {
			return nil, err
		}
		if err = health(ctx, c, file); err != nil {
			return nil, err
		}
		backendLog(c, "reuse", old.Process.PID)
		return &old, nil
	}
	if err = removeStaleListener(c, &old); err != nil {
		return nil, err
	}
	args, err := backendArgs(c, file)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(c.CLI, args...)
	detachedCommand(cmd)
	blocked := map[string]bool{}
	for _, key := range []string{"CODEX_CLI_PATH", "CODEX_HOME", "LAZYMIND_CODEX_DESKTOP_CONFIG", "CODEX_APP_SERVER_FORCE_CLI", "CODEX_APP_SERVER_WS_URL", "CODEX_APP_SERVER_USE_LOCAL_DAEMON", "CODEX_APP_TOOLS_PIPE_PATH", "CODEX_TASK_WORKSPACE_VERIFYING_IDENTITY", "BROWSER_USE_ACCESS_VERIFYING_IDENTITY"} {
		blocked[key] = true
	}
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if !blocked[strings.ToUpper(key)] {
			cmd.Env = append(cmd.Env, value)
		}
	}
	if c.Helper != "" {
		cmd.Env = append(cmd.Env, "LAZYMIND_RUNTIME_HELPER="+c.Helper)
	}
	cmd.Env = append(cmd.Env, "CODEX_CLI_PATH="+c.CLI, "CODEX_HOME="+c.CodexHome, "LAZYMIND_CODEX_DESKTOP_CONFIG="+file)
	log, err := os.OpenFile(filepath.Join(c.RuntimeDir, "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer log.Close()
	cmd.Stdout = log
	cmd.Stderr = log
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	pid := cmd.Process.Pid
	// Wait reaps a failed child during readiness; a successful daemon is released.
	identity, err := platformProcess(pid)
	if err != nil || identity == nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, errors.New("new backend identity unavailable")
	}
	record := &backendRecord{Process: identity, Endpoint: c.Endpoint}
	success := false
	defer func() {
		if !success {
			if sameProcess(identity) {
				_ = cmd.Process.Kill()
			}
			_ = cmd.Wait()
		} else {
			_ = cmd.Process.Release()
		}
	}()
	if err = writePrivateJSON(recordPath, record); err != nil {
		return nil, err
	}
	backendLog(c, "spawn", pid)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if !sameProcess(identity) {
			return nil, errors.New("backend exited during startup")
		}
		socket, e := listenerIdentity(c)
		if e == nil && socket != nil {
			record.Socket = socket
			if e = writePrivateJSON(recordPath, record); e != nil {
				return nil, e
			}
			if e = verifyListener(c, record); e == nil {
				if e = health(ctx, c, file); e == nil {
					success = true
					backendLog(c, "ready", pid)
					return record, nil
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, errors.New("backend startup timed out; no private fallback")
}

// JSON-round-trip comparison keeps socket records stable across persisted reads.
func equalRecord(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	var aa, bb any
	_ = json.Unmarshal(x, &aa)
	_ = json.Unmarshal(y, &bb)
	return reflect.DeepEqual(aa, bb)
}
