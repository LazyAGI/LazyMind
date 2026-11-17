package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/cli/cli/v2/pkg/cmd/auth/shared"
	"github.com/cli/cli/v2/pkg/iostreams"
)

// The official CLI owns OAuth. Its config interface hands the result directly
// to memory instead of activating a host profile or writing credentials.
type memoryConfig struct {
	token string
}

func (*memoryConfig) UsersForHost(string) []string { return nil }

func (config *memoryConfig) Login(host, user, token, protocol string, _ bool) (bool, error) {
	if host != "github.com" || user == "" || protocol != "https" || len(token) == 0 || len(token) > 4096 {
		return false, errors.New("GITHUB_CLI_OUTPUT_INVALID")
	}
	for _, character := range token {
		if character <= ' ' || character >= 127 {
			return false, errors.New("GITHUB_CLI_OUTPUT_INVALID")
		}
	}
	config.token = token
	return false, nil
}

func cliVersion() string {
	if build, ok := debug.ReadBuildInfo(); ok {
		for _, dependency := range build.Deps {
			if dependency.Path == "github.com/cli/cli/v2" && dependency.Replace == nil {
				return strings.TrimPrefix(dependency.Version, "v")
			}
		}
	}
	return "unknown"
}

func run() int {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Fprintf(os.Stdout, "lazymind github-auth-helper %s (memory)\n", cliVersion())
		return 0
	}
	if len(os.Args) != 1 {
		return 1
	}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		key = strings.ToUpper(key)
		if strings.HasPrefix(key, "GH_") || strings.HasPrefix(key, "GITHUB_") || strings.HasPrefix(key, "GIT_CONFIG") ||
			key == "GIT_EXEC_PATH" || key == "GIT_ASKPASS" || key == "SSH_ASKPASS" {
			return 1
		}
	}
	// API host resolution in the CLI library consults go-gh config even with
	// an explicit HTTP client. The null device prevents host config/cache reads.
	for _, key := range []string{"GH_CONFIG_DIR", "GH_CACHE_DIR"} {
		if err := os.Setenv(key, os.DevNull); err != nil {
			return 1
		}
	}
	input, err := os.Open(os.DevNull)
	if err != nil {
		return 1
	}
	defer input.Close()
	// The service holds this pipe open. Its exit or crash stops the OAuth
	// process without leaving a temporary token file or a background login.
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(1)
	}()
	streams := iostreams.System()
	streams.In, streams.Out, streams.ErrOut = input, os.Stderr, os.Stderr
	streams.ContentOut = os.Stderr
	streams.SetStdinTTY(false)
	streams.SetStdoutTTY(false)
	streams.SetStderrTTY(false)
	streams.SetColorEnabled(false)
	streams.SetNeverPrompt(true)
	config := &memoryConfig{}
	client := &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone(), Timeout: 35 * time.Second}
	err = shared.Login(&shared.LoginOptions{
		IO: streams, Config: config, HTTPClient: client, PlainHTTPClient: client,
		Hostname: "github.com", GitProtocol: "https", Web: true,
		SkipSSHKeyPrompt: true, CredentialFlow: &shared.GitCredentialFlow{},
	})
	if err != nil || config.token == "" {
		// Never print library errors: upstream errors can contain response data.
		fmt.Fprintln(os.Stderr, "GITHUB_AUTH_FAILED")
		return 1
	}
	result := struct {
		AccessToken string `json:"access_token"`
	}{AccessToken: config.token}
	if json.NewEncoder(os.Stdout).Encode(result) != nil {
		return 1
	}
	return 0
}

func main() { os.Exit(run()) }
