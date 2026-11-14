package systemdeps

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"lazymind/core/common"
)

const managedNodeVersion = "24.22.3"

var nodeInstallMu sync.Mutex
var nodeChecksums = map[string]string{
	"darwin-arm64": "ff6e17d4874b76d272212b908cc020d6e815fba5a6c7eaaa06add725840307fd",
	"darwin-x64":   "977614af0504c81045e7fd85596dbca0e5f951b96681516dadafedcca8c447f7",
	"linux-arm64":  "261af2af250dfd7e9f25cf151d1b2713a0adf8f10a9ad116c4947f68627affe8",
	"linux-x64":    "061ec3e3062115705b56b51fa9cc330ab50d34b6969387d4e4b1a50c6809acc2",
	"win-arm64":    "b3cc74a295b39d4f273271e96f58c06dd2b35757ca12abec64c7d0598b434f14",
	"win-x64":      "e612af99f626ab415ea225af6bce5c971feb448cb5cae250bb1c3fa50c5f7d9f",
}

type NodeDependencyStatus struct {
	Installed        bool   `json:"installed"`
	Version          string `json:"version,omitempty"`
	Path             string `json:"path,omitempty"`
	InstallSupported bool   `json:"installSupported"`
}

func nodePlatform() string {
	platform, arch := runtime.GOOS, runtime.GOARCH
	if platform == "windows" {
		platform = "win"
	}
	if arch == "amd64" {
		arch = "x64"
	}
	return platform + "-" + arch
}

func managedNodePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	name := "node"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(home, ".lazymind", "deps", "node", "bin", name), nil
}

func compatibleNodeVersion(version string) bool {
	var major, minor int
	_, err := fmt.Sscanf(strings.TrimSpace(version), "v%d.%d", &major, &minor)
	return err == nil && (major > 22 || major == 22 && minor >= 19)
}

func detectNode(ctx context.Context) NodeDependencyStatus {
	status := NodeDependencyStatus{InstallSupported: IsLocalRuntime() && nodeChecksums[nodePlatform()] != ""}
	managed, _ := managedNodePath()
	// LAZYMIND_NODE_EXECUTABLE may be Electron for PPT export. The connector
	// needs a standalone Node.js executable and cannot use that desktop runtime.
	candidates := []string{"node", managed}
	// Match the connector's explicit override: installation cannot fix an invalid override.
	if override := strings.TrimSpace(os.Getenv("LAZYMIND_NODE_BIN")); override != "" {
		candidates = []string{override}
		status.InstallSupported = false
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		path, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		output, err := exec.CommandContext(probe, path, "--version").Output()
		cancel()
		if err == nil && compatibleNodeVersion(string(output)) {
			status.Installed, status.Version, status.Path = true, strings.TrimSpace(string(output)), path
			break
		}
	}
	return status
}

func GetNodeDependency(w http.ResponseWriter, r *http.Request) {
	if !IsLocalRuntime() {
		common.ReplyErr(w, "Node.js dependency is only available locally", http.StatusForbidden)
		return
	}
	common.ReplyOK(w, detectNode(r.Context()))
}

func InstallNodeDependency(w http.ResponseWriter, r *http.Request) {
	if !IsLocalRuntime() {
		common.ReplyErr(w, "Node.js installation is only available locally", http.StatusForbidden)
		return
	}
	nodeInstallMu.Lock()
	defer nodeInstallMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	status := detectNode(ctx)
	if status.Installed {
		common.ReplyOK(w, status)
		return
	}
	if !status.InstallSupported {
		common.ReplyErr(w, "Node.js installation is unsupported; check platform and LAZYMIND_NODE_BIN", http.StatusBadRequest)
		return
	}
	if err := installManagedNode(ctx); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	status = detectNode(ctx)
	if !status.Installed {
		common.ReplyErr(w, "Node.js installation verification failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, status)
}

func installManagedNode(ctx context.Context) error {
	target, err := managedNodePath()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(target), ".install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	prefix := "node-v" + managedNodeVersion + "-" + nodePlatform()
	extension := ".tar.gz"
	member := prefix + "/bin/node"
	if runtime.GOOS == "windows" {
		extension = ".zip"
		member = prefix + "/node.exe"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://nodejs.org/dist/v"+managedNodeVersion+"/"+prefix+extension, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download Node.js: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download Node.js: HTTP %d", response.StatusCode)
	}
	archive := filepath.Join(stage, "download")
	file, err := os.Create(archive)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, 128<<20))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if fmt.Sprintf("%x", hash.Sum(nil)) != nodeChecksums[nodePlatform()] {
		return fmt.Errorf("Node.js download checksum mismatch")
	}
	executable := filepath.Join(stage, filepath.Base(target))
	if err := extractNodeExecutable(archive, member, executable, extension == ".zip"); err != nil {
		return err
	}
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(probe, executable, "--version").Output()
	if err != nil || strings.TrimSpace(string(output)) != "v"+managedNodeVersion {
		return fmt.Errorf("downloaded Node.js verification failed")
	}
	return os.Rename(executable, target)
}

// Extract only the executable, never archive-controlled paths or links.
func extractNodeExecutable(archive, member, destination string, zipped bool) error {
	write := func(reader io.Reader) error {
		file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
		if err != nil {
			return err
		}
		_, err = io.Copy(file, reader)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
	if zipped {
		reader, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer reader.Close()
		for _, file := range reader.File {
			if file.Name != member || !file.Mode().IsRegular() {
				continue
			}
			content, err := file.Open()
			if err != nil {
				return err
			}
			defer content.Close()
			return write(content)
		}
	} else {
		file, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer file.Close()
		gz, err := gzip.NewReader(file)
		if err != nil {
			return err
		}
		defer gz.Close()
		reader := tar.NewReader(gz)
		for {
			header, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if header.Name == member && header.Typeflag == tar.TypeReg {
				return write(reader)
			}
		}
	}
	return fmt.Errorf("Node.js executable missing from archive")
}
