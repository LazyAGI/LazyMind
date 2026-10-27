package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualHelperDiscoverAndDownloadCommands(t *testing.T) {
	f := newHelperProcessFixture(t)
	f.config.Mode = "unsigned-test"
	f.config.Audience = "personal-network"
	f.config.Channel = "personal-test"
	var signedPolicy []byte
	var holdPackage atomic.Bool
	started := make(chan struct{}, 2)
	files := map[string][]byte{"/package": []byte("synthetic candidate archive"), "/feed": []byte("synthetic immutable feed"), "/components": []byte("synthetic component catalog")}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/policy.json" {
			_, _ = w.Write(signedPolicy)
			return
		}
		if body, ok := files[r.URL.Path]; ok {
			if r.URL.Path == "/package" && holdPackage.Load() {
				w.Header().Set("Content-Length", fmt.Sprint(len(body)))
				_, _ = w.Write(body[:1])
				w.(http.Flusher).Flush()
				started <- struct{}{}
				<-r.Context().Done()
				return
			}
			_, _ = w.Write(body)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	f.config.PolicyURL = server.URL + "/policy.json"
	f.config.AllowedOrigins = append(f.config.AllowedOrigins, server.URL)
	var request map[string]json.RawMessage
	if err := json.Unmarshal(f.request(42), &request); err != nil {
		t.Fatal(err)
	}
	var envelope map[string]string
	if err := json.Unmarshal(request["envelope"], &envelope); err != nil {
		t.Fatal(err)
	}
	payload, err := base64.StdEncoding.DecodeString(envelope["payload"])
	if err != nil {
		t.Fatal(err)
	}
	var policy map[string]any
	if err := json.Unmarshal(payload, &policy); err != nil {
		t.Fatal(err)
	}
	current := policy["releases"].([]any)[0].(map[string]any)
	rawCurrent, _ := json.Marshal(current)
	var target map[string]any
	_ = json.Unmarshal(rawCurrent, &target)
	target["releaseId"] = "candidate"
	target["version"] = "1.1.0"
	for kind, body := range files {
		sum := sha256.Sum256(body)
		target[strings.TrimPrefix(kind, "/")] = map[string]any{"url": server.URL + kind, "size": len(body), "sha256": hex.EncodeToString(sum[:])}
	}
	policy["releases"] = append(policy["releases"].([]any), target)
	policy["edges"] = []any{map[string]any{"sourceReleaseId": "current", "targetReleaseId": "candidate", "priority": 0, "migrationProfile": "same-v1", "sourceDataProfiles": []string{"known-v1"}, "targetDataProfile": "known-v1", "requiredComponents": map[string]string{}, "recoveryContract": map[string]any{"version": 1, "sourcePackageSHA256": current["package"].(map[string]any)["sha256"], "sourceManifestSHA256": strings.Repeat("b", 64), "helperProtocolMin": 1, "helperProtocolMax": 1, "direction": "target-to-source"}}}
	payload, _ = json.Marshal(policy)
	signedPolicy, _ = json.Marshal(map[string]string{"keyId": "key", "payload": base64.StdEncoding.EncodeToString(payload), "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(f.private, payload))})
	body, _ := json.Marshal(map[string]any{"state": json.RawMessage(request["state"]), "releaseId": "candidate"})
	check := f.command(t, "discover")
	check.Stdin = bytes.NewReader(body)
	raw, err := check.Output()
	if err != nil {
		t.Fatalf("discover entrypoint: %s %v", raw, err)
	}
	var decision map[string]any
	if err := json.Unmarshal(raw, &decision); err != nil {
		t.Fatal(err)
	}
	if decision["status"] != "direct" {
		t.Fatalf("discover result: %s", raw)
	}
	download := f.command(t, "download")
	stdin, err := download.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	go func() { _, _ = stdin.Write(append(body, '\n')) }()
	raw, err = download.Output()
	if err != nil {
		t.Fatalf("download entrypoint: %s %v", raw, err)
	}
	lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
	if len(lines) < 2 {
		t.Fatalf("missing streamed progress/final receipt: %s", raw)
	}
	var receipt map[string]any
	if err := json.Unmarshal(lines[len(lines)-1], &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt["status"] != "downloaded" || receipt["releaseId"] != "candidate" {
		t.Fatalf("wrong final receipt: %s", raw)
	}
	path, ok := receipt["packagePath"].(string)
	if !ok {
		t.Fatal("receipt missing package path")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(files["/package"]) {
		t.Fatalf("wrong final artifact: %q %v", data, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	holdPackage.Store(true)
	for _, control := range []string{"cancel", "parent EOF"} {
		t.Run(control, func(t *testing.T) {
			cmd := f.command(t, "download")
			input, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			output, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			if _, err := input.Write(append(append([]byte{}, body...), '\n')); err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("package stream did not start")
			}
			cancelStarted := time.Now()
			if control == "cancel" {
				_, _ = input.Write([]byte("{\"cancel\":true}\n"))
			} else {
				_ = input.Close()
			}
			// cancel keeps stdin open: an EOF-only implementation must fail.
			cancelOutput, _ := io.ReadAll(output)
			waitErr := cmd.Wait()
			var exitErr *exec.ExitError
			if !errors.As(waitErr, &exitErr) || exitErr.ExitCode() != 1 || time.Since(cancelStarted) > 3*time.Second {
				t.Fatalf("did not exit normally within cleanup bound: %v, %s", waitErr, cancelOutput)
			}
			lines := bytes.Split(bytes.TrimSpace(cancelOutput), []byte("\n"))
			var final map[string]any
			if err := json.Unmarshal(lines[len(lines)-1], &final); err != nil || final["status"] != "check_failed" || final["reason"] != "cancelled" {
				t.Fatalf("missing explicit cancellation receipt: %s", cancelOutput)
			}
			if err := filepath.WalkDir(f.root, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !d.IsDir() && (strings.HasSuffix(d.Name(), ".part") || strings.HasPrefix(d.Name(), ".download-")) {
					return fmt.Errorf("partial file survived graceful cancellation: %s", p)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
