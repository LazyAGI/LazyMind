package systemdeps

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"lazymind/core/common"
	appLog "lazymind/core/log"
)

type PythonComponent struct {
	ID              string            `json:"id"`
	Revision        string            `json:"revision"`
	BaseFingerprint string            `json:"baseFingerprint"`
	Platform        string            `json:"platform"`
	Arch            string            `json:"arch"`
	PythonABI       string            `json:"pythonAbi"`
	Filename        string            `json:"filename"`
	SHA256          string            `json:"sha256"`
	URL             string            `json:"url"`
	FallbackURL     string            `json:"fallbackUrl,omitempty"`
	SizeBytes       int64             `json:"sizeBytes"`
	UnpackedBytes   int64             `json:"unpackedBytes"`
	Packages        map[string]string `json:"packages"`
}

type pythonCatalog struct {
	SchemaVersion int                        `json:"schemaVersion"`
	Platform      string                     `json:"platform"`
	Arch          string                     `json:"arch"`
	Components    map[string]PythonComponent `json:"components"`
}

type PythonComponentStatus struct {
	ID               string `json:"id"`
	Installed        bool   `json:"installed"`
	Active           bool   `json:"active"`
	RestartRequired  bool   `json:"restartRequired"`
	InstallSupported bool   `json:"installSupported"`
	Installing       bool   `json:"installing"`
	Filename         string `json:"filename,omitempty"`
	URL              string `json:"url,omitempty"`
	FallbackURL      string `json:"fallbackUrl,omitempty"`
	SizeBytes        int64  `json:"sizeBytes,omitempty"`
	UnpackedBytes    int64  `json:"unpackedBytes,omitempty"`
	Message          string `json:"message,omitempty"`
}

var pythonComponentLocks = map[string]*sync.Mutex{"rag": {}}
var pythonComponentDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func loadPythonCatalog() (*pythonCatalog, error) {
	path := os.Getenv("LAZYMIND_PYTHON_COMPONENT_CATALOG")
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var catalog pythonCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, err
	}
	if catalog.SchemaVersion != 1 || catalog.Platform != runtime.GOOS || catalog.Arch != runtime.GOARCH {
		return nil, errors.New("Python dependency catalog does not match this platform")
	}
	for _, id := range []string{"rag"} {
		entry, ok := catalog.Components[id]
		if !ok || entry.ID != id || !pythonComponentDigest.MatchString(entry.Revision) ||
			!pythonComponentDigest.MatchString(entry.SHA256) || !pythonComponentDigest.MatchString(entry.BaseFingerprint) ||
			entry.Platform != catalog.Platform || entry.Arch != catalog.Arch || entry.PythonABI == "" ||
			entry.SizeBytes <= 0 || entry.UnpackedBytes <= 0 {
			return nil, fmt.Errorf("invalid Python dependency descriptor: %s", id)
		}
	}
	return &catalog, nil
}

func pythonComponentDir(root string, entry PythonComponent) string {
	return filepath.Join(root, "deps", "python-components", entry.ID, entry.Revision)
}

func validatePythonComponent(dir string, entry PythonComponent) error {
	data, err := os.ReadFile(filepath.Join(dir, "bundle-manifest.json"))
	if err != nil {
		return err
	}
	var installed PythonComponent
	if err := json.Unmarshal(data, &installed); err != nil {
		return err
	}
	if installed.ID != entry.ID || installed.Revision != entry.Revision ||
		installed.BaseFingerprint != entry.BaseFingerprint || installed.Platform != entry.Platform ||
		installed.Arch != entry.Arch || installed.PythonABI != entry.PythonABI {
		return errors.New("dependency bundle is incompatible with this app version/platform")
	}
	if info, err := os.Stat(filepath.Join(dir, "site-packages")); err != nil || !info.IsDir() {
		return errors.New("dependency bundle has no site-packages")
	}
	return nil
}

func pythonComponentInstalled(root string, entry PythonComponent) bool {
	dir := pythonComponentDir(root, entry)
	marker, err := os.ReadFile(filepath.Join(dir, ".ready"))
	return err == nil && strings.TrimSpace(string(marker)) == entry.SHA256 && validatePythonComponent(dir, entry) == nil
}

func PythonComponentActive(id string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("LAZYMIND_" + strings.ToUpper(id) + "_ENABLED")))
	return value != "0" && value != "false"
}

func pythonComponentStatus(root string, catalog *pythonCatalog, id string) PythonComponentStatus {
	status := PythonComponentStatus{ID: id, Installed: true, Active: true}
	if catalog == nil {
		return status // Source/cloud and legacy full packages.
	}
	entry := catalog.Components[id]
	status.Filename, status.URL, status.FallbackURL = entry.Filename, entry.URL, entry.FallbackURL
	status.SizeBytes, status.UnpackedBytes = entry.SizeBytes, entry.UnpackedBytes
	status.InstallSupported = IsLocalRuntime() && os.Getenv("LAZYMIND_ALGORITHM_PYTHON") != ""
	status.Installed = pythonComponentInstalled(root, entry)
	status.Active = status.Installed && PythonComponentActive(id)
	status.RestartRequired = status.Installed && !status.Active
	if lock := pythonComponentLocks[id]; lock.TryLock() {
		lock.Unlock()
	} else {
		status.Installing = true
	}
	return status
}

func downloadPythonComponent(ctx context.Context, entry PythonComponent, address, destination string) error {
	fallback := entry.FallbackURL
	// Internal tests can supply a local source. The public API always uses the catalog.
	if address != entry.URL {
		fallback = ""
	}
	return downloadMirroredAsset(ctx, &http.Client{Timeout: 30 * time.Minute}, address, fallback,
		entry.Filename, destination, entry.SizeBytes, entry.SHA256, primaryMirrorPolicy)
}

func extractPythonComponent(ctx context.Context, archive, destination string, maxBytes int64) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer reader.Close()
	var total uint64
	for _, item := range reader.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.ContainsAny(item.Name, `\:`) || item.Mode()&os.ModeSymlink != 0 || (item.Name != "bundle-manifest.json" && !strings.HasPrefix(item.Name, "site-packages/")) {
			return errors.New("unexpected dependency archive entry")
		}
		target, err := safeEditablePPTArchiveTarget(destination, item.Name)
		if err != nil {
			return err
		}
		if item.UncompressedSize64 > uint64(maxBytes)+1024*1024-total {
			return errors.New("dependency archive exceeds expected expanded size")
		}
		total += item.UncompressedSize64
		if item.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		input, err := item.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, item.Mode().Perm()|0o600)
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func verifyPythonComponent(ctx context.Context, dir string, entry PythonComponent) error {
	python := os.Getenv("LAZYMIND_ALGORITHM_PYTHON")
	if python == "" {
		return errors.New("bundled Python is unavailable")
	}
	code := `import os, site, sys
source = os.path.join(sys.argv[3], "lazyllm")
if os.path.isdir(os.path.join(source, "lazyllm")):
    sys.path.insert(0, source)
site.addsitedir(sys.argv[1])
import lazyllm.tools.rag, pymilvus, milvus_lite, spacy
`
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(checkCtx, python, "-I", "-B", "-c", code, filepath.Join(dir, "site-packages"), entry.ID, os.Getenv("LAZYMIND_ALGORITHM_SOURCE"))
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("dependency import verification failed: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func installPythonComponent(ctx context.Context, root string, entry PythonComponent, address string) (installErr error) {
	if pythonComponentInstalled(root, entry) {
		appLog.Logger.Info().Str("component", entry.ID).Str("revision", entry.Revision).Msg("python_component.install.reused")
		return nil
	}
	deps := filepath.Join(root, "deps", "python-components")
	if err := os.MkdirAll(deps, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(deps, ".install-"+entry.ID+"-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	archive := filepath.Join(stage, "component.zip")
	started := time.Now()
	appLog.Logger.Info().Str("component", entry.ID).Str("revision", entry.Revision).Str("filename", entry.Filename).Msg("python_component.download.started")
	defer func() {
		if installErr != nil {
			event := "python_component.install.failed"
			if ctx.Err() != nil {
				event = "python_component.install.cancelled"
			}
			appLog.Logger.Warn().Err(installErr).Str("component", entry.ID).Dur("elapsed", time.Since(started)).Msg(event)
		} else {
			appLog.Logger.Info().Str("component", entry.ID).Str("revision", entry.Revision).Dur("elapsed", time.Since(started)).Msg("python_component.install.completed")
		}
	}()

	if err := downloadPythonComponent(ctx, entry, address, archive); err != nil {
		return err
	}
	payload := filepath.Join(stage, "payload")
	if err := extractPythonComponent(ctx, archive, payload, entry.UnpackedBytes); err != nil {
		return err
	}
	if err := validatePythonComponent(payload, entry); err != nil {
		return err
	}
	if err := verifyPythonComponent(ctx, payload, entry); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(payload, ".ready"), []byte(entry.SHA256+"\n"), 0o644); err != nil {
		return err
	}
	target := pythonComponentDir(root, entry)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	backup := filepath.Join(stage, "previous")
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, backup); err != nil {
			return err
		}
	}
	if err := os.Rename(payload, target); err != nil {
		_ = os.Rename(backup, target)
		return err
	}
	return nil
}

func GetPythonComponents(w http.ResponseWriter, r *http.Request) {
	catalog, err := loadPythonCatalog()
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	root, _ := RuntimeRootFromEnv()
	statuses := []PythonComponentStatus{}
	for _, id := range []string{"rag"} {
		statuses = append(statuses, pythonComponentStatus(root, catalog, id))
	}
	common.ReplyOK(w, statuses)
}

func InstallPythonComponent(w http.ResponseWriter, r *http.Request) {
	if !IsLocalRuntime() {
		common.ReplyErr(w, "dependency installation is only supported in desktop/local mode", http.StatusForbidden)
		return
	}
	var request struct {
		ID string `json:"id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		common.ReplyErr(w, "invalid dependency install request", http.StatusBadRequest)
		return
	}
	lock, ok := pythonComponentLocks[request.ID]
	if !ok {
		common.ReplyErr(w, "unknown Python dependency", http.StatusBadRequest)
		return
	}
	if !lock.TryLock() {
		common.ReplyErr(w, "dependency installation is already running", http.StatusConflict)
		return
	}
	defer lock.Unlock()
	catalog, err := loadPythonCatalog()
	if err != nil || catalog == nil {
		common.ReplyErr(w, "dependency catalog is unavailable", http.StatusServiceUnavailable)
		return
	}
	root, err := RuntimeRootFromEnv()
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	entry := catalog.Components[request.ID]
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Minute)
	defer cancel()
	if err := installPythonComponent(ctx, root, entry, entry.URL); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	status := pythonComponentStatus(root, catalog, request.ID)
	status.Installing = false
	common.ReplyOK(w, status)
}

func RequireRAG(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !PythonComponentActive("rag") {
			common.ReplyErrWithData(w, "请先安装本地知识库组件，并重启本地服务。", map[string]string{
				"code": "CAPABILITY_REQUIRED", "component": "rag",
				"settings_url": "/settings?section=system_tools#python-rag-dependency",
			}, http.StatusServiceUnavailable)
			return
		}
		next(w, r)
	}
}
