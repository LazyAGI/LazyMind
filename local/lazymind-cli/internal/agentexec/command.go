package agentexec

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type cappedBuffer struct {
	bytes.Buffer
	Limit int
}

func (b *cappedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	if remaining := b.Limit - b.Len(); remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = b.Buffer.Write(value)
	}
	return original, nil
}

type StreamCommand struct {
	Binary       string
	Arguments    []string
	Directory    string
	Environment  []string
	Stdin        io.Reader
	MaxLineBytes int
}

func (spec StreamCommand) Run(ctx context.Context, handle func([]byte) error) error {
	if strings.TrimSpace(spec.Binary) == "" || handle == nil {
		return errors.New("stream command and line handler are required")
	}
	command, invocationEnvironment, err := commandContext(ctx, spec.Binary, spec.Arguments...)
	if err != nil {
		return err
	}
	command.Dir = spec.Directory
	if spec.Environment == nil {
		command.Env = SafeEnvironment(invocationEnvironment...)
	} else {
		command.Env = append(append([]string(nil), spec.Environment...), invocationEnvironment...)
	}
	command.Stdin = spec.Stdin
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	stderrBuffer := cappedBuffer{Limit: 64 << 10}
	stderrDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&stderrBuffer, stderr)
		close(stderrDone)
	}()
	maxLineBytes := spec.MaxLineBytes
	if maxLineBytes <= 0 {
		maxLineBytes = 4 << 20
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), maxLineBytes)
	var handleErr error
	for scanner.Scan() {
		if err := handle(scanner.Bytes()); err != nil {
			handleErr = err
			_ = command.Process.Kill()
			break
		}
	}
	scanErr := scanner.Err()
	waitErr := command.Wait()
	<-stderrDone
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if handleErr != nil {
		return handleErr
	}
	if scanErr != nil {
		return fmt.Errorf("read process output: %w", scanErr)
	}
	if waitErr != nil {
		message := strings.TrimSpace(stderrBuffer.String())
		if message == "" {
			message = waitErr.Error()
		}
		return errors.New(message)
	}
	return nil
}

// SafeEnvironment keeps the operating-system and provider authentication
// variables needed by supported Agent CLIs without exposing unrelated service
// credentials inherited by the LazyMind desktop process.
func SafeEnvironment(additional ...string) []string {
	allowed := map[string]bool{
		"HOME": true, "USER": true, "LOGNAME": true, "SHELL": true, "PATH": true,
		"TMPDIR": true, "TEMP": true, "TMP": true, "LANG": true, "TERM": true,
		"USERPROFILE": true, "APPDATA": true, "LOCALAPPDATA": true, "PROGRAMDATA": true,
		"SystemRoot": true, "SYSTEMROOT": true, "ComSpec": true, "COMSPEC": true, "PATHEXT": true,
		"COLORTERM": true, "NO_COLOR": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "NO_PROXY": true,
		"http_proxy": true, "https_proxy": true, "all_proxy": true, "no_proxy": true,
		"CODEX_HOME": true, "OPENAI_API_KEY": true, "OPENAI_BASE_URL": true,
		"CURSOR_API_KEY": true, "CURSOR_API_ENDPOINT": true,
		"CODEBUDDY_API_KEY": true, "CODEBUDDY_BASE_URL": true,
		"ANTHROPIC_API_KEY": true, "ANTHROPIC_BASE_URL": true,
	}
	if runtime.GOOS == "windows" {
		normalized := make(map[string]bool, len(allowed))
		for name := range allowed {
			normalized[strings.ToUpper(name)] = true
		}
		allowed = normalized
	}
	environment := make([]string, 0, len(allowed)+len(additional))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if runtime.GOOS == "windows" {
			name = strings.ToUpper(name)
		}
		if allowed[name] || strings.HasPrefix(name, "LC_") {
			environment = append(environment, entry)
		}
	}
	return platformSafeEnvironment(append(environment, additional...))
}

func Find(configured, environment string, names, candidates []string) (string, error) {
	resolved, err := FindExecutable(configured, environment, names, candidates)
	if err != nil {
		return "", err
	}
	return ResolveRunnable(resolved)
}

func FindBound(configured, environment string, target BindingTarget, names, candidates []string) (string, error) {
	configured, err := configuredExecutable(configured, environment, target)
	if err != nil {
		return "", err
	}
	return Find(configured, "", names, candidates)
}

func FindBoundExecutable(configured, environment string, target BindingTarget, names, candidates []string) (string, error) {
	configured, err := configuredExecutable(configured, environment, target)
	if err != nil {
		return "", err
	}
	return FindExecutable(configured, "", names, candidates)
}

func FindExecutable(configured, environment string, names, candidates []string) (string, error) {
	if strings.TrimSpace(configured) == "" && environment != "" {
		configured = strings.TrimSpace(os.Getenv(environment))
	}
	if strings.TrimSpace(configured) != "" {
		return ResolveExecutable(configured)
	}
	for _, name := range names {
		if resolved, err := exec.LookPath(name); err == nil {
			return ResolveExecutable(resolved)
		}
	}
	for _, candidate := range platformExecutableCandidates(names) {
		if resolved, err := ResolveExecutable(candidate); err == nil {
			return resolved, nil
		}
	}
	for _, candidate := range candidates {
		if resolved, err := ResolveExecutable(candidate); err == nil {
			return resolved, nil
		}
	}
	return "", errors.New("executable is not installed")
}

func ResolveRunnable(candidate string) (string, error) {
	resolved, err := ResolveExecutable(candidate)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Run(ctx, resolved, "--version"); err != nil {
		return "", err
	}
	return resolved, nil
}

func ResolveExecutable(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("executable path is empty")
	}
	if resolved, handled, err := resolvePlatformExecutable(value); handled {
		if err != nil {
			return "", err
		}
		value = resolved
	} else if !filepath.IsAbs(value) {
		resolved, err := exec.LookPath(value)
		if err != nil {
			return "", err
		}
		value = resolved
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", abs)
	}
	return filepath.Clean(abs), nil
}

func Run(ctx context.Context, binary string, arguments ...string) (string, error) {
	command, invocationEnvironment, err := commandContext(ctx, binary, arguments...)
	if err != nil {
		return "", err
	}
	command.Env = SafeEnvironment(invocationEnvironment...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		if message == "" {
			message = err.Error()
		}
		return "", errors.New(message)
	}
	return stdout.String(), nil
}

func SameExecutable(left, right string) bool {
	resolvedLeft, leftErr := ResolveExecutable(left)
	resolvedRight, rightErr := ResolveExecutable(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(resolvedLeft, resolvedRight)
	}
	return resolvedLeft == resolvedRight
}

func ConnectorRuntime() (string, string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", "", fmt.Errorf("resolve LazyMind executable: %w", err)
	}
	self, err = ResolveExecutable(self)
	if err != nil {
		return "", "", fmt.Errorf("resolve LazyMind executable: %w", err)
	}
	home, err := lazyMindHome()
	if err != nil {
		return "", "", err
	}
	return self, home, nil
}

func LazyMindHome() (string, error) { return lazyMindHome() }

func PersistentHostID() (string, error) {
	home, err := lazyMindHome()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, "connector-host-id")
	if body, err := os.ReadFile(path); err == nil {
		if value := strings.TrimSpace(string(body)); strings.HasPrefix(value, "host-") && len(value) == 37 {
			return value, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	id := "host-" + hex.EncodeToString(value)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		body, readErr := os.ReadFile(path)
		return strings.TrimSpace(string(body)), readErr
	}
	if err != nil {
		return "", err
	}
	if _, err := file.WriteString(id + "\n"); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return id, nil
}

func LazyMindMCPConfig(executable, home, externalRef, conversationID, leaseToken, hostID string) ([]byte, error) {
	config := struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}{MCPServers: map[string]struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}{
		"lazymind": {
			Command: executable,
			Args:    []string{"mcp", "proxy"},
			Env: map[string]string{
				"LAZYMIND_HOME":            home,
				"LAZYMIND_EXTERNAL_REF":    externalRef,
				"LAZYMIND_EXTERNAL_LEASE":  leaseToken,
				"LAZYMIND_EXTERNAL_HOST":   hostID,
				"LAZYMIND_CONVERSATION_ID": conversationID,
			},
		},
	}}
	body, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode LazyMind MCP configuration: %w", err)
	}
	return body, nil
}

func EnsureConversationWorkspace(conversationID string) (string, error) {
	home, err := lazyMindHome()
	if err != nil {
		return "", err
	}
	root := filepath.Join(home, "agent-workspaces")
	name := strings.TrimSpace(conversationID)
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return "", errors.New("valid conversation ID is required")
	}
	workspace := filepath.Join(root, name)
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		return "", fmt.Errorf("create Agent workspace: %w", err)
	}
	return workspace, nil
}

func lazyMindHome() (string, error) {
	home := strings.TrimSpace(os.Getenv("LAZYMIND_HOME"))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve LazyMind home: %w", err)
		}
		home = filepath.Join(userHome, ".lazymind")
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return "", fmt.Errorf("resolve LazyMind home: %w", err)
	}
	return filepath.Clean(home), nil
}
