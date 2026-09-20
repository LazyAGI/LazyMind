package providerconnection

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	defaultFeishuCLIOutputLimit   = int64(2 << 20)
	defaultFeishuCLICommandTimout = 30 * time.Second
)

var (
	ErrCLIUnavailable        = errors.New("CLI_UNAVAILABLE")
	ErrCLINotInstalled       = errors.New("CLI_NOT_INSTALLED")
	ErrCLIVersionUnsupported = errors.New("CLI_VERSION_UNSUPPORTED")
	ErrCLIIntegrityMismatch  = errors.New("CLI_INTEGRITY_MISMATCH")
	ErrCLIOutputInvalid      = errors.New("CLI_OUTPUT_INVALID")
	ErrCLITimeout            = errors.New("CLI_TIMEOUT")
)

type FeishuCLIRunner struct {
	binaryPath     string
	expectedSHA256 string
	outputLimit    int64
	commandTimeout time.Duration
}

type FeishuCLIJSONResult struct {
	ExitCode int
}

type FeishuCLIProcessResult struct {
	ExitCode int
	Stdout   []byte
	Err      error
}

type FeishuCLICommandError struct {
	Code string
}

func (err *FeishuCLICommandError) Error() string { return "Feishu CLI command failed" }

type FeishuCLIProcess struct {
	VerificationURL <-chan string
	Done            <-chan FeishuCLIProcessResult
	cancel          context.CancelFunc
}

func (process *FeishuCLIProcess) Cancel() {
	if process != nil && process.cancel != nil {
		process.cancel()
	}
}

type FeishuCLIAuthStart struct {
	VerificationURL string `json:"verification_url"`
	DeviceCode      string `json:"-"`
	ExpiresIn       int    `json:"expires_in"`
}

type feishuCLIAuthStartWire struct {
	VerificationURL string `json:"verification_url"`
	DeviceCode      string `json:"device_code"`
	ExpiresIn       int    `json:"expires_in"`
	Hint            string `json:"hint"`
}

type FeishuCLIAuthComplete struct {
	Event          string          `json:"event"`
	UserOpenID     string          `json:"user_open_id"`
	UserName       string          `json:"user_name"`
	Scope          string          `json:"scope"`
	Requested      []string        `json:"requested"`
	NewlyGranted   []string        `json:"newly_granted"`
	AlreadyGranted []string        `json:"already_granted"`
	Missing        []string        `json:"missing"`
	Granted        []string        `json:"granted"`
	Warning        json.RawMessage `json:"warning"`
}

type FeishuCLIAuthStatus struct {
	AppID       string `json:"appId"`
	Brand       string `json:"brand"`
	DefaultAs   string `json:"defaultAs"`
	Identity    string `json:"identity"`
	Verified    bool   `json:"verified"`
	VerifyError string `json:"verifyError"`
	Note        string `json:"note"`
	Identities  struct {
		User struct {
			Available        bool   `json:"available"`
			Status           string `json:"status"`
			Verified         *bool  `json:"verified"`
			Message          string `json:"message"`
			Hint             string `json:"hint"`
			UserName         string `json:"userName"`
			OpenID           string `json:"openId"`
			TokenStatus      string `json:"tokenStatus"`
			Scope            string `json:"scope"`
			ExpiresAt        string `json:"expiresAt"`
			RefreshExpiresAt string `json:"refreshExpiresAt"`
			GrantedAt        string `json:"grantedAt"`
		} `json:"user"`
		Bot json.RawMessage `json:"bot"`
	} `json:"identities"`
}

type FeishuCLIAuthCheck struct {
	OK      bool     `json:"ok"`
	Granted []string `json:"granted"`
	Missing []string `json:"missing"`
}

type FeishuCLIUserIdentity struct {
	Name      string `json:"name"`
	OpenID    string `json:"open_id"`
	UnionID   string `json:"union_id"`
	UserID    string `json:"user_id"`
	TenantKey string `json:"tenant_key"`
}

type feishuCLIUserIdentityWire struct {
	Name         string `json:"name"`
	EnglishName  string `json:"en_name"`
	OpenID       string `json:"open_id"`
	UnionID      string `json:"union_id"`
	UserID       string `json:"user_id"`
	TenantKey    string `json:"tenant_key"`
	AvatarBig    string `json:"avatar_big"`
	AvatarMiddle string `json:"avatar_middle"`
	AvatarThumb  string `json:"avatar_thumb"`
	AvatarURL    string `json:"avatar_url"`
}

type FeishuCLIAPIResult struct {
	Data json.RawMessage
}

func NewFeishuCLIRunner(binaryPath, expectedSHA256 string) (*FeishuCLIRunner, error) {
	binaryPath = filepath.Clean(strings.TrimSpace(binaryPath))
	expectedSHA256 = strings.ToLower(strings.TrimSpace(expectedSHA256))
	if !filepath.IsAbs(binaryPath) {
		return nil, ErrCLINotInstalled
	}
	info, err := os.Stat(binaryPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return nil, ErrCLINotInstalled
	}
	if len(expectedSHA256) != sha256.Size*2 {
		return nil, ErrCLIIntegrityMismatch
	}
	payload, err := os.ReadFile(binaryPath)
	if err != nil {
		return nil, ErrCLIUnavailable
	}
	digest := sha256.Sum256(payload)
	for index := range payload {
		payload[index] = 0
	}
	if hex.EncodeToString(digest[:]) != expectedSHA256 {
		return nil, ErrCLIIntegrityMismatch
	}
	return &FeishuCLIRunner{
		binaryPath: binaryPath, expectedSHA256: expectedSHA256,
		outputLimit: defaultFeishuCLIOutputLimit, commandTimeout: defaultFeishuCLICommandTimout,
	}, nil
}

func (runner *FeishuCLIRunner) VerifyVersion(ctx context.Context, profileDir string) error {
	stdout, _, err := runner.run(ctx, profileDir, runner.commandTimeout, "--version")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(stdout)) != "lark-cli version "+FeishuCLIVersion {
		return ErrCLIVersionUnsupported
	}
	return nil
}

func (runner *FeishuCLIRunner) StartConfigInit(ctx context.Context, profileDir string) (*FeishuCLIProcess, error) {
	if runner == nil || !validFeishuCLIProfileDirectory(profileDir) {
		return nil, ErrCLIUnavailable
	}
	if err := prepareFeishuCLIProfileDataDirectory(profileDir); err != nil {
		return nil, err
	}
	processContext, cancel := context.WithCancel(ctx)
	command := exec.CommandContext(processContext, runner.binaryPath, "config", "init", "--new", "--brand", "feishu")
	command.Dir = profileDir
	command.Env = runner.environment(profileDir)
	stdout := &boundedBuffer{limit: runner.outputLimit}
	stderr := &boundedBuffer{limit: runner.outputLimit}
	verification := &verificationURLWriter{limit: runner.outputLimit, found: make(chan string, 1)}
	command.Stdout = io.MultiWriter(stdout, verification)
	command.Stderr = io.MultiWriter(stderr, verification)
	if err := command.Start(); err != nil {
		cancel()
		return nil, ErrCLIUnavailable
	}
	done := make(chan FeishuCLIProcessResult, 1)
	go func() {
		err := command.Wait()
		classified := classifyFeishuCLIProcessError(
			processContext,
			err,
			stdout.overflowed() || stderr.overflowed() || verification.overflowed(),
		)
		if classified != nil && !errors.Is(classified, context.Canceled) {
			classified = classifyFeishuCLIOutputError(stderr.Bytes(), "APP_CREATION_DENIED", classified)
			classified = classifyFeishuCLIOutputError(stdout.Bytes(), "APP_CREATION_DENIED", classified)
		}
		result := FeishuCLIProcessResult{Stdout: stdout.Bytes(), Err: classified}
		if command.ProcessState != nil {
			result.ExitCode = command.ProcessState.ExitCode()
		}
		done <- result
		close(done)
		verification.close()
		cancel()
	}()
	return &FeishuCLIProcess{VerificationURL: verification.found, Done: done, cancel: cancel}, nil
}

func (runner *FeishuCLIRunner) AuthLoginStart(ctx context.Context, profileDir string, scopes []string) (FeishuCLIAuthStart, error) {
	if len(scopes) == 0 {
		return FeishuCLIAuthStart{}, ErrCLIOutputInvalid
	}
	for _, scope := range scopes {
		if !validFeishuCLIScope(scope) {
			return FeishuCLIAuthStart{}, ErrCLIOutputInvalid
		}
	}
	var wire feishuCLIAuthStartWire
	_, err := runner.RunJSON(ctx, profileDir, []string{feishuCLIAuthLoginCommand[0], feishuCLIAuthLoginCommand[1], "--scope", strings.Join(scopes, " "), feishuCLIAuthNoWaitFlag, "--json"}, &wire)
	if err != nil {
		return FeishuCLIAuthStart{}, err
	}
	if !validFeishuVerificationURL(wire.VerificationURL) || strings.TrimSpace(wire.DeviceCode) == "" || wire.ExpiresIn < 30 || wire.ExpiresIn > 1800 {
		return FeishuCLIAuthStart{}, ErrCLIOutputInvalid
	}
	return FeishuCLIAuthStart{VerificationURL: wire.VerificationURL, DeviceCode: wire.DeviceCode, ExpiresIn: wire.ExpiresIn}, nil
}

func (runner *FeishuCLIRunner) AuthLoginComplete(ctx context.Context, profileDir, deviceCode string) (FeishuCLIAuthComplete, error) {
	if strings.TrimSpace(deviceCode) == "" || len(deviceCode) > 4096 {
		return FeishuCLIAuthComplete{}, ErrCLIOutputInvalid
	}
	var output FeishuCLIAuthComplete
	stdout, _, err := runner.run(ctx, profileDir, 10*time.Minute, feishuCLIAuthLoginCommand[0], feishuCLIAuthLoginCommand[1], feishuCLIDeviceCodeFlag, deviceCode, "--json")
	if err != nil {
		if decodeErr := decodeFeishuCLIEnvelope(stdout, &output); decodeErr == nil && output.Event == "authorization_complete" && len(output.Missing) > 0 {
			return FeishuCLIAuthComplete{}, &FeishuCLICommandError{Code: "AUTH_WAITING_ADMIN"}
		}
		return FeishuCLIAuthComplete{}, err
	}
	if err := decodeFeishuCLIEnvelope(stdout, &output); err != nil {
		return FeishuCLIAuthComplete{}, err
	}
	if output.Event != "authorization_complete" || strings.TrimSpace(output.UserOpenID) == "" || len(output.Missing) != 0 {
		return FeishuCLIAuthComplete{}, ErrCLIOutputInvalid
	}
	return output, nil
}

func (runner *FeishuCLIRunner) AuthStatus(ctx context.Context, profileDir string) (FeishuCLIAuthStatus, error) {
	var output FeishuCLIAuthStatus
	_, err := runner.RunJSON(ctx, profileDir, []string{"auth", "status", "--json", "--verify"}, &output)
	if err != nil {
		return FeishuCLIAuthStatus{}, err
	}
	if output.Identity != "user" || !output.Verified || !output.Identities.User.Available || strings.TrimSpace(output.Identities.User.OpenID) == "" {
		return FeishuCLIAuthStatus{}, ErrCLIOutputInvalid
	}
	return output, nil
}

func (runner *FeishuCLIRunner) AuthCheck(ctx context.Context, profileDir string, scopes []string) (FeishuCLIAuthCheck, error) {
	normalized := normalizeFeishuCLIScopes(scopes)
	if len(normalized) == 0 {
		return FeishuCLIAuthCheck{}, ErrCLIOutputInvalid
	}
	var output FeishuCLIAuthCheck
	_, err := runner.RunJSON(ctx, profileDir, []string{"auth", "check", "--scope", strings.Join(normalized, " "), "--json"}, &output)
	if err != nil {
		return FeishuCLIAuthCheck{}, err
	}
	granted := make(map[string]struct{}, len(output.Granted))
	for _, scope := range output.Granted {
		if !validFeishuCLIScope(scope) {
			return FeishuCLIAuthCheck{}, ErrCLIOutputInvalid
		}
		granted[scope] = struct{}{}
	}
	if !output.OK || len(output.Missing) != 0 {
		return FeishuCLIAuthCheck{}, &FeishuCLICommandError{Code: "AUTH_SCOPE_MISSING"}
	}
	for _, scope := range normalized {
		if _, found := granted[scope]; !found {
			return FeishuCLIAuthCheck{}, &FeishuCLICommandError{Code: "AUTH_SCOPE_MISSING"}
		}
	}
	output.Granted = append([]string(nil), normalized...)
	return output, nil
}

func (runner *FeishuCLIRunner) ResolveUserIdentity(ctx context.Context, profileDir string) (FeishuCLIUserIdentity, error) {
	result, err := runner.RunAPI(ctx, profileDir, "/open-apis/authen/v1/user_info", nil)
	if err != nil {
		return FeishuCLIUserIdentity{}, err
	}
	var wire feishuCLIUserIdentityWire
	if err := decodeFeishuCLIEnvelope(result.Data, &wire); err != nil || strings.TrimSpace(wire.OpenID) == "" || strings.TrimSpace(wire.TenantKey) == "" {
		return FeishuCLIUserIdentity{}, ErrCLIOutputInvalid
	}
	return FeishuCLIUserIdentity{
		Name: wire.Name, OpenID: wire.OpenID, UnionID: wire.UnionID,
		UserID: wire.UserID, TenantKey: wire.TenantKey,
	}, nil
}

func (runner *FeishuCLIRunner) RunAPI(ctx context.Context, profileDir, path string, params map[string]any) (FeishuCLIAPIResult, error) {
	if !validFeishuCLIAPIPath(path) {
		return FeishuCLIAPIResult{}, ErrCLIOutputInvalid
	}
	args := []string{"api", "GET", path}
	if len(params) > 0 {
		encoded, err := json.Marshal(params)
		if err != nil || len(encoded) > 32<<10 {
			return FeishuCLIAPIResult{}, ErrCLIOutputInvalid
		}
		args = append(args, "--params", string(encoded))
	}
	args = append(args, "--as", "user", "--format", "json")
	stdout, _, err := runner.run(ctx, profileDir, runner.commandTimeout, args...)
	if err != nil {
		return FeishuCLIAPIResult{}, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(stdout, &raw); err != nil {
		return FeishuCLIAPIResult{}, ErrCLIOutputInvalid
	}
	for key := range raw {
		if key != "ok" && key != "identity" && key != "data" && key != "meta" && key != "_notice" {
			return FeishuCLIAPIResult{}, ErrCLIOutputInvalid
		}
	}
	var ok bool
	var identity string
	if json.Unmarshal(raw["ok"], &ok) != nil || json.Unmarshal(raw["identity"], &identity) != nil || !ok || identity != "user" || len(raw["data"]) == 0 {
		return FeishuCLIAPIResult{}, ErrCLIOutputInvalid
	}
	return FeishuCLIAPIResult{Data: append(json.RawMessage(nil), raw["data"]...)}, nil
}

func (runner *FeishuCLIRunner) DownloadFile(ctx context.Context, profile FeishuCLIProfile, fileToken string) ([]byte, error) {
	if !validFeishuCLIObjectToken(fileToken) || !validFeishuCLIProfileDirectory(profile.ConfigDir) || !validFeishuCLIProfileDirectory(profile.DownloadsDir) {
		return nil, ErrCLIOutputInvalid
	}
	name := "download-" + randomFeishuCLIIdentifier() + ".bin"
	outputPath := filepath.Join(profile.DownloadsDir, name)
	if !strings.HasPrefix(filepath.Clean(outputPath), filepath.Clean(profile.DownloadsDir)+string(filepath.Separator)) {
		return nil, ErrCLIOutputInvalid
	}
	defer func() { _ = os.Remove(outputPath) }()
	args := []string{"drive", "+download", "--file-token", fileToken, "--output", outputPath, "--overwrite", "--as", "user", "--format", "json"}
	if _, _, err := runner.run(ctx, profile.ConfigDir, 2*time.Minute, args...); err != nil {
		return nil, err
	}
	file, err := os.Open(outputPath)
	if err != nil {
		return nil, ErrCLIUnavailable
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, 100<<20+1))
	if err != nil || len(content) == 0 || len(content) > 100<<20 {
		return nil, ErrCLIOutputInvalid
	}
	return content, nil
}

func EncodeFeishuCLIDownload(content []byte) string {
	return base64.StdEncoding.EncodeToString(content)
}

func (runner *FeishuCLIRunner) RunJSON(ctx context.Context, profileDir string, args []string, output any) (FeishuCLIJSONResult, error) {
	if output == nil || !allowedFeishuCLICommand(args) {
		return FeishuCLIJSONResult{}, ErrCLIOutputInvalid
	}
	stdout, _, err := runner.run(ctx, profileDir, runner.commandTimeout, args...)
	if err != nil {
		return FeishuCLIJSONResult{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return FeishuCLIJSONResult{}, ErrCLIOutputInvalid
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return FeishuCLIJSONResult{}, ErrCLIOutputInvalid
	}
	return FeishuCLIJSONResult{}, nil
}

func (runner *FeishuCLIRunner) run(ctx context.Context, profileDir string, timeout time.Duration, args ...string) ([]byte, []byte, error) {
	if runner == nil || !validFeishuCLIProfileDirectory(profileDir) || !allowedFeishuCLICommand(args) {
		return nil, nil, ErrCLIUnavailable
	}
	if err := prepareFeishuCLIProfileDataDirectory(profileDir); err != nil {
		return nil, nil, err
	}
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(commandContext, runner.binaryPath, args...)
	command.Dir = profileDir
	command.Env = runner.environment(profileDir)
	stdout := &boundedBuffer{limit: runner.outputLimit}
	stderr := &boundedBuffer{limit: runner.outputLimit}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	classified := classifyFeishuCLIProcessError(commandContext, err, stdout.overflowed() || stderr.overflowed())
	if classified != nil {
		classified = classifyFeishuCLIOutputError(stderr.Bytes(), "CLI_UNAVAILABLE", classified)
		classified = classifyFeishuCLIOutputError(stdout.Bytes(), "CLI_UNAVAILABLE", classified)
		return stdout.Bytes(), stderr.Bytes(), classified
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}

func (runner *FeishuCLIRunner) environment(profileDir string) []string {
	environment := make([]string, 0, len(os.Environ())+4)
	for _, item := range os.Environ() {
		key := item
		if index := strings.IndexByte(item, '='); index >= 0 {
			key = item[:index]
		}
		switch key {
		case "HOME", "LARKSUITE_CLI_CONFIG_DIR", "LARKSUITE_CLI_DATA_DIR", "LARKSUITE_CLI_NO_UPDATE_NOTIFIER", "LARKSUITE_CLI_NO_SKILLS_NOTIFIER":
			continue
		}
		environment = append(environment, item)
	}
	dataDir, _ := feishuCLIProfileDataDirectory(profileDir)
	homeDir, _ := feishuCLIProfileHomeDirectory(profileDir)
	// lark-cli 1.0.93 uses DATA_DIR on Linux but derives its macOS encrypted-file directory from HOME.
	return append(environment,
		"HOME="+homeDir,
		"LARKSUITE_CLI_CONFIG_DIR="+profileDir,
		"LARKSUITE_CLI_DATA_DIR="+dataDir,
		"LARKSUITE_CLI_NO_UPDATE_NOTIFIER=1",
		"LARKSUITE_CLI_NO_SKILLS_NOTIFIER=1",
	)
}

func prepareFeishuCLIProfileDataDirectory(profileDir string) error {
	dataDir, ok := feishuCLIProfileDataDirectory(profileDir)
	if !ok {
		return ErrCLIUnavailable
	}
	homeDir, ok := feishuCLIProfileHomeDirectory(profileDir)
	if !ok {
		return ErrCLIUnavailable
	}
	for _, path := range []string{dataDir, homeDir} {
		if err := secureMkdirAll(path); err != nil {
			return ErrCLIUnavailable
		}
	}
	return nil
}

func feishuCLIProfileDataDirectory(profileDir string) (string, bool) {
	configDir := filepath.Clean(strings.TrimSpace(profileDir))
	if !filepath.IsAbs(configDir) || configDir == string(filepath.Separator) || filepath.Base(configDir) != "config" {
		return "", false
	}
	profileRoot := filepath.Dir(configDir)
	if profileRoot == string(filepath.Separator) || profileRoot == configDir {
		return "", false
	}
	return filepath.Join(profileRoot, "state", "cli-data"), true
}

func feishuCLIProfileHomeDirectory(profileDir string) (string, bool) {
	configDir := filepath.Clean(strings.TrimSpace(profileDir))
	if !filepath.IsAbs(configDir) || configDir == string(filepath.Separator) || filepath.Base(configDir) != "config" {
		return "", false
	}
	profileRoot := filepath.Dir(configDir)
	if profileRoot == string(filepath.Separator) || profileRoot == configDir {
		return "", false
	}
	return filepath.Join(profileRoot, "state", "home"), true
}

func allowedFeishuCLICommand(args []string) bool {
	if len(args) == 1 && args[0] == "--version" {
		return true
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--"+"recommend") || strings.Contains(joined, "--domain all") {
		return false
	}
	if len(args) >= 4 && args[0] == "auth" && args[1] == "login" {
		return args[len(args)-1] == "--json" && (containsArgument(args, "--no-wait") || containsArgument(args, "--device-code"))
	}
	if len(args) == 4 && args[0] == "auth" && args[1] == "status" && args[2] == "--json" && args[3] == "--verify" {
		return true
	}
	if len(args) == 5 && args[0] == "auth" && args[1] == "check" && args[2] == "--scope" && args[4] == "--json" {
		return len(normalizeFeishuCLIScopes(strings.Fields(args[3]))) > 0
	}
	if len(args) == 3 && args[0] == "auth" && args[1] == "logout" && args[2] == "--json" {
		return true
	}
	if len(args) >= 7 && args[0] == "api" && args[1] == "GET" && validFeishuCLIAPIPath(args[2]) && containsArgument(args, "--as") && containsArgument(args, "user") {
		return true
	}
	if len(args) >= 8 && args[0] == "drive" && args[1] == "+download" && containsArgument(args, "--as") && containsArgument(args, "user") && containsArgument(args, "--output") {
		return true
	}
	return false
}

func containsArgument(args []string, expected string) bool {
	for _, argument := range args {
		if argument == expected {
			return true
		}
	}
	return false
}

func validFeishuCLIScope(scope string) bool {
	if scope == "offline_access" {
		return true
	}
	if len(scope) < 3 || len(scope) > 128 || strings.ContainsAny(scope, " ,\t\r\n") {
		return false
	}
	for _, character := range scope {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == ':' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func validFeishuCLIObjectToken(value string) bool {
	if len(value) < 1 || len(value) > 256 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validFeishuCLICursor(value string) bool {
	if len(value) < 1 || len(value) > 2048 || strings.ContainsAny(value, "\r\n\t ") {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("-._~+/=", character) {
			continue
		}
		return false
	}
	return true
}

func validFeishuCLIAPIPath(path string) bool {
	if len(path) < len("/open-apis/") || len(path) > 1024 || !strings.HasPrefix(path, "/open-apis/") || strings.ContainsAny(path, "?#\\\r\n") {
		return false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(path, "/open-apis/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		for _, character := range segment {
			if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-' || character == '.' {
				continue
			}
			return false
		}
	}
	return true
}

func validFeishuCLIProfileDirectory(path string) bool {
	path = filepath.Clean(strings.TrimSpace(path))
	return filepath.IsAbs(path) && path != string(filepath.Separator)
}

func validFeishuVerificationURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" || parsed.Port() != "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "accounts.feishu.cn", "accounts.larksuite.com":
		return parsed.Path == "/oauth/v1/device/verify"
	case "open.feishu.cn", "open.larksuite.com":
		return parsed.Path == "/page/cli"
	default:
		return false
	}
}

func classifyFeishuCLIProcessError(ctx context.Context, err error, overflow bool) error {
	if overflow {
		return ErrCLIOutputInvalid
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrCLITimeout
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return context.Canceled
	}
	if err != nil {
		return ErrCLIUnavailable
	}
	return nil
}

type boundedBuffer struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	limit    int64
	overflow bool
}

func (buffer *boundedBuffer) Write(payload []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	if buffer.limit <= 0 || int64(buffer.buffer.Len()+len(payload)) > buffer.limit {
		buffer.overflow = true
		return len(payload), nil
	}
	return buffer.buffer.Write(payload)
}

func (buffer *boundedBuffer) Bytes() []byte {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return append([]byte(nil), buffer.buffer.Bytes()...)
}

func (buffer *boundedBuffer) overflowed() bool {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.overflow
}

var verificationURLPattern = regexp.MustCompile(`https://[^\s\x1b]+`)

type verificationURLWriter struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	limit    int64
	overflow bool
	found    chan string
	once     sync.Once
}

func (writer *verificationURLWriter) Write(payload []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.limit <= 0 || int64(writer.buffer.Len()+len(payload)) > writer.limit {
		writer.overflow = true
		return len(payload), nil
	}
	_, _ = writer.buffer.Write(payload)
	for _, candidate := range verificationURLPattern.FindAllString(writer.buffer.String(), -1) {
		candidate = strings.TrimRight(candidate, ")]}>,.;")
		if validFeishuVerificationURL(candidate) {
			writer.once.Do(func() { writer.found <- candidate })
			break
		}
	}
	return len(payload), nil
}

func (writer *verificationURLWriter) overflowed() bool {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.overflow
}

func (writer *verificationURLWriter) Bytes() []byte {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return append([]byte(nil), writer.buffer.Bytes()...)
}

func (writer *verificationURLWriter) close() {
	writer.once.Do(func() {})
	close(writer.found)
}

func safeFeishuCLIErrorCode(err error) string {
	var commandError *FeishuCLICommandError
	if errors.As(err, &commandError) && commandError.Code != "" {
		return commandError.Code
	}
	switch {
	case errors.Is(err, ErrCLINotInstalled):
		return "CLI_NOT_INSTALLED"
	case errors.Is(err, ErrCLIVersionUnsupported):
		return "CLI_VERSION_UNSUPPORTED"
	case errors.Is(err, ErrCLIIntegrityMismatch):
		return "CLI_INTEGRITY_MISMATCH"
	case errors.Is(err, ErrCLIOutputInvalid):
		return "CLI_OUTPUT_INVALID"
	case errors.Is(err, ErrCLITimeout), errors.Is(err, context.DeadlineExceeded):
		return "CLI_TIMEOUT"
	case errors.Is(err, context.Canceled):
		return "AUTH_CANCELED"
	default:
		return "CLI_UNAVAILABLE"
	}
}

func classifyFeishuCLIOutputError(payload []byte, fallbackCode string, fallback error) error {
	var envelope struct {
		OK    bool `json:"ok"`
		Error struct {
			Type    string          `json:"type"`
			Subtype string          `json:"subtype"`
			Code    json.RawMessage `json:"code"`
		} `json:"error"`
	}
	if len(payload) == 0 || json.Unmarshal(payload, &envelope) != nil || envelope.OK {
		return fallback
	}
	code := fallbackCode
	switch strings.ToLower(strings.TrimSpace(envelope.Error.Subtype)) {
	case "missing_scope", "approval_required":
		code = "AUTH_WAITING_ADMIN"
	case "token_missing", "token_expired", "invalid_token":
		code = "AUTH_TOKEN_EXPIRED"
	case "not_configured", "invalid_client":
		code = "APP_CREATION_FORBIDDEN"
	case "permission_denied", "forbidden":
		code = "AUTH_SCOPE_MISSING"
	case "canceled", "access_denied", "user_denied":
		code = "AUTH_CANCELED"
	}
	return &FeishuCLICommandError{Code: code}
}

func decodeFeishuCLIEnvelope(payload []byte, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("%w", ErrCLIOutputInvalid)
	}
	return nil
}
