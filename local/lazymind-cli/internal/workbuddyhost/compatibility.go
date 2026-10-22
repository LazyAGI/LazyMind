package workbuddyhost

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"lazymind/agentconnector/internal/agentexec"
)

const MinimumVersion = "5.8.3"

type CompatibilityError struct{ Code string }

func (e *CompatibilityError) Error() string { return "WorkBuddy: " + e.Code }

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// Compare with a stable minimum. A prerelease of the minimum is still too old.
func VersionIssue(version string) string {
	m := versionPattern.FindStringSubmatch(version)
	if m == nil {
		return "version_unreadable"
	}
	for _, part := range strings.Split(m[4], ".") {
		if len(part) > 1 && part[0] == '0' && strings.Trim(part, "0123456789") == "" {
			return "version_unreadable"
		}
	}
	values := [3]uint64{}
	for i := range values {
		v, err := strconv.ParseUint(m[i+1], 10, 53)
		if err != nil {
			return "version_unreadable"
		}
		values[i] = v
	}
	for i, minimum := range [3]uint64{5, 8, 3} {
		if values[i] < minimum {
			return "unsupported_version"
		}
		if values[i] > minimum {
			return ""
		}
	}
	if m[4] != "" {
		return "unsupported_version"
	}
	return ""
}

func desktopApplication() (string, error) {
	return agentexec.FindDesktopApplication(agentexec.DesktopApplication{
		BindingTarget: agentexec.WorkBuddyDesktop, DisplayNames: []string{"WorkBuddy", "WorkBuddy CN"},
		ExecutableNames: []string{"WorkBuddy.exe", "WorkBuddy CN.exe"},
	})
}

// Inspect the desktop package, never the bundled CLI (which has its own version).
// Both macOS and Windows ship the Electron manifest in resources/app.asar.
func Preflight() error {
	application, err := desktopApplication()
	if err != nil {
		return fmt.Errorf("%w: %v", &CompatibilityError{Code: "host_unavailable"}, err)
	}
	version, err := desktopVersion(application)
	if err != nil {
		return fmt.Errorf("%w: %v", &CompatibilityError{Code: "version_unreadable"}, err)
	}
	if issue := VersionIssue(version); issue != "" {
		return fmt.Errorf("%w: desktop version %s", &CompatibilityError{Code: issue}, version)
	}
	return nil
}

func desktopVersion(application string) (string, error) {
	resources := filepath.Join(filepath.Dir(application), "resources")
	if strings.EqualFold(filepath.Ext(application), ".app") {
		resources = filepath.Join(application, "Contents", "Resources")
	}
	f, err := os.Open(filepath.Join(resources, "app.asar"))
	if err != nil {
		return "", err
	}
	defer f.Close()
	// ASAR pickle header: size pickle, header pickle, then file bytes. Bound all
	// metadata reads and offsets; never extract or execute archive contents.
	var prefix [16]byte
	if _, err = io.ReadFull(f, prefix[:]); err != nil {
		return "", err
	}
	headerSize := int64(binary.LittleEndian.Uint32(prefix[4:8]))
	jsonSize := int64(binary.LittleEndian.Uint32(prefix[12:16]))
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if binary.LittleEndian.Uint32(prefix[:4]) != 4 || headerSize < 8 || headerSize > 16<<20 || jsonSize < 2 || jsonSize > headerSize-8 || 8+headerSize > info.Size() {
		return "", errors.New("invalid ASAR header")
	}
	header := make([]byte, int(jsonSize))
	if _, err = io.ReadFull(f, header); err != nil {
		return "", err
	}
	var index struct {
		Files map[string]struct {
			Size     int64  `json:"size"`
			Offset   string `json:"offset"`
			Unpacked bool   `json:"unpacked"`
			Link     string `json:"link"`
		} `json:"files"`
	}
	if err = json.Unmarshal(header, &index); err != nil {
		return "", err
	}
	entry, ok := index.Files["package.json"]
	offset, err := strconv.ParseInt(entry.Offset, 10, 64)
	base := 8 + headerSize
	if !ok || err != nil || offset < 0 || entry.Unpacked || entry.Link != "" || entry.Size < 2 || entry.Size > 1<<20 || offset > info.Size()-base || entry.Size > info.Size()-base-offset {
		return "", errors.New("invalid ASAR manifest")
	}
	body := make([]byte, int(entry.Size))
	if _, err = f.ReadAt(body, base+offset); err != nil {
		return "", err
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err = json.Unmarshal(body, &manifest); err != nil {
		return "", err
	}
	return manifest.Version, nil
}

func StatusIssue(status Status) string {
	switch status.Issue {
	case "unsupported_version", "version_unreadable", "capability_unavailable", "host_unavailable":
		return status.Issue
	}
	if status.Version != "" || status.Ready {
		return VersionIssue(status.Version)
	}
	return ""
}
