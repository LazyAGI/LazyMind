package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

var embeddedConfig string
var resolveRecoveryBase = desktopupdate.RecoveryBase

// Windows trust must remain machine-local: Roaming profiles can be replaced
// outside the helper's file lock by profile synchronization at login/logout.
func recoveryBaseForPlatform(platform string, getenv func(string) string, userConfigDir func() (string, error)) (string, error) {
	return desktopupdate.RecoveryBaseForPlatform(platform, getenv, userConfigDir)
}

// embeddedConfig is populated with -X at build time. The runtime never reads
// trust keys, policy URLs or the trust-root location from command-line flags.
func main() {
	signals, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	limit := 30 * time.Second
	if len(os.Args) == 2 && os.Args[1] == "download" {
		limit = 2 * time.Hour
	}
	ctx, cancel := context.WithTimeout(signals, limit)
	defer cancel()
	base, err := resolveRecoveryBase()
	root := filepath.Join(base, "LazyMindUpdater")
	if desktopupdate.UnsignedTestBuild && !(len(os.Args) == 2 && os.Args[1] == "config") {
		root, err = desktopupdate.UpdaterRoot()
	}
	if err == nil {
		err = run(ctx, os.Args[1:], os.Stdin, os.Stdout, embeddedConfig, root, time.Now().UTC())
	}
	if err != nil {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "check_failed", "reason": desktopupdate.ErrorCode(err)})
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, input io.Reader, output io.Writer, encodedConfig, trustRoot string, now time.Time) error {
	if len(args) == 1 && args[0] == "recovery-status" {
		journals, err := desktopupdate.ReadUpdateJournals(ctx, trustRoot)
		if err != nil {
			return err
		}
		type summary struct {
			ID            string `json:"id"`
			Stage         string `json:"stage"`
			SourceVersion string `json:"sourceVersion"`
			TargetVersion string `json:"targetVersion"`
		}
		transactions := []summary{}
		for _, j := range journals {
			if j.Stage != "COMMITTED" && j.Stage != "RECOVERED" && j.Stage != "ABORTED" {
				transactions = append(transactions, summary{j.ID, j.Stage, j.Plan.SourceVersion, j.Plan.TargetVersion})
			}
		}
		return json.NewEncoder(output).Encode(struct {
			Status       string    `json:"status"`
			Transactions []summary `json:"transactions"`
		}{"recovery_status", transactions})
	}
	if len(args) != 1 || (args[0] != "config" && args[0] != "check" && args[0] != "discover" && args[0] != "download") {
		return fmt.Errorf("usage: update-helper config|check|discover|download")
	}
	if encodedConfig == "" {
		return json.NewEncoder(output).Encode(map[string]string{"status": "disabled", "reason": "not_configured"})
	}
	raw, err := base64.StdEncoding.DecodeString(encodedConfig)
	if err != nil {
		return &desktopupdate.Failure{Code: "configuration_invalid", Cause: err}
	}
	var config desktopupdate.Config
	if len(raw) > desktopupdate.MaxEnvelopeBytes {
		return &desktopupdate.Failure{Code: "configuration_invalid"}
	}
	if err := decodeInput(raw, &config); err != nil {
		return &desktopupdate.Failure{Code: "configuration_invalid", Cause: err}
	}
	if err := desktopupdate.ValidateConfig(config); err != nil {
		return err
	}
	if args[0] == "config" {
		return json.NewEncoder(output).Encode(struct {
			Status string               `json:"status"`
			Config desktopupdate.Config `json:"config"`
		}{"enabled", config})
	}
	var request struct {
		Envelope        json.RawMessage          `json:"envelope"`
		State           desktopupdate.LocalState `json:"state"`
		RequireExisting bool                     `json:"requireExisting"`
		ReleaseID       string                   `json:"releaseId"`
	}
	// The envelope itself is bounded again by the verifier. This outer bound
	// also prevents an oversized local state from exhausting the helper.
	var data []byte
	var control *bufio.Scanner
	if args[0] == "download" {
		control = bufio.NewScanner(input)
		control.Buffer(make([]byte, 4096), 2*desktopupdate.MaxEnvelopeBytes)
		if !control.Scan() {
			return &desktopupdate.Failure{Code: "request_invalid", Cause: control.Err()}
		}
		data = append([]byte{}, control.Bytes()...)
	} else {
		data, err = io.ReadAll(io.LimitReader(input, 2*desktopupdate.MaxEnvelopeBytes+1))
	}
	if err != nil {
		return err
	}
	if len(data) > 2*desktopupdate.MaxEnvelopeBytes {
		return &desktopupdate.Failure{Code: "request_too_large"}
	}
	if err := decodeInput(data, &request); err != nil {
		return &desktopupdate.Failure{Code: "request_invalid", Cause: err}
	}
	if args[0] == "download" {
		downloadCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		// One bounded control message (or EOF when the parent disappears) ends
		// this operation. No general-purpose process or filesystem command exists.
		go func() {
			if control.Scan() {
				var message struct {
					Cancel bool `json:"cancel"`
				}
				_ = decodeInput(control.Bytes(), &message)
			}
			cancel()
		}()
		encoder := json.NewEncoder(output)
		receipt, err := desktopupdate.Download(downloadCtx, config, request.State, trustRoot, request.ReleaseID, nil, now, func(progress desktopupdate.Progress) {
			if err := encoder.Encode(progress); err != nil {
				cancel()
			}
		})
		if err != nil {
			return err
		}
		return encoder.Encode(receipt)
	}
	var decision desktopupdate.Decision
	if args[0] == "discover" {
		decision, err = desktopupdate.Discover(ctx, config, request.State, trustRoot, nil, now)
	} else {
		decision, err = desktopupdate.Check(ctx, request.Envelope, config, request.State, trustRoot, request.RequireExisting, now)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(decision)
}

func decodeInput(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("extra JSON value")
	}
	return nil
}
