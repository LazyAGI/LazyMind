package cloudpackage

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
)

const FormatSchemaV2 = "lazymind.resource-manifest/v2"

const maxManifestFiles = 10_000
const maxManifestFileBytes int64 = 100 << 20
const maxManifestContentBytes int64 = 500 << 20

type File struct {
	Data       []byte
	Executable bool
}

type ManifestFile struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	Executable bool   `json:"executable,omitempty"`
}

type Manifest struct {
	FormatSchema         string         `json:"format_schema"`
	ResourceType         string         `json:"resource_type"`
	ResourceName         string         `json:"resource_name"`
	ClientResourceKey    string         `json:"client_resource_key"`
	Entrypoint           string         `json:"entrypoint"`
	Files                []ManifestFile `json:"files"`
	ContentSize          int64          `json:"content_size"`
	ContentHash          string         `json:"content_hash"`
	TransportFormat      string         `json:"transport_format"`
	TransportSize        int64          `json:"transport_size"`
	TransportHash        string         `json:"transport_hash"`
	MinDesktopVersion    string         `json:"min_desktop_version"`
	RequiredCapabilities []string       `json:"required_capabilities"`
	Permissions          []string       `json:"permissions"`
}

type PrepareInput struct {
	ResourceType      string
	ResourceName      string
	ClientResourceKey string
	DesktopVersion    string
	Files             map[string]File
}

type Prepared struct {
	Manifest Manifest
	Files    map[string]File
	ZIPPath  string
}

func Prepare(input PrepareInput) (Prepared, error) {
	resourceType := strings.TrimSpace(input.ResourceType)
	entrypoint, required, err := packageShape(resourceType)
	if err != nil {
		return Prepared{}, err
	}
	resourceName := strings.TrimSpace(input.ResourceName)
	clientResourceKey := strings.TrimSpace(input.ClientResourceKey)
	desktopVersion := strings.TrimSpace(input.DesktopVersion)
	if !validResourceName(resourceName) || !validClientResourceKey(clientResourceKey) || !validDesktopVersion(desktopVersion) {
		return Prepared{}, errors.New("cloud resource metadata is incomplete")
	}
	if len(input.Files) == 0 || len(input.Files) > maxManifestFiles {
		return Prepared{}, errors.New("cloud resource package is empty")
	}
	files := make(map[string]File, len(input.Files))
	paths := make([]string, 0, len(input.Files))
	for rawPath, file := range input.Files {
		clean, err := cleanPath(rawPath)
		if err != nil || clean != rawPath {
			return Prepared{}, fmt.Errorf("unsafe cloud resource path %q", rawPath)
		}
		if len(clean) > 1024 || int64(len(file.Data)) > maxManifestFileBytes {
			return Prepared{}, fmt.Errorf("cloud resource file exceeds limits: %s", clean)
		}
		if file.Executable && !strings.HasPrefix(clean, "scripts/") {
			return Prepared{}, fmt.Errorf("executable file is outside scripts: %s", clean)
		}
		file.Data = append([]byte(nil), file.Data...)
		files[clean] = file
		paths = append(paths, clean)
	}
	for _, requiredPath := range required {
		if _, ok := files[requiredPath]; !ok {
			return Prepared{}, fmt.Errorf("cloud %s package must contain %s", resourceType, requiredPath)
		}
	}
	sort.Strings(paths)
	manifestFiles := make([]ManifestFile, 0, len(paths))
	var contentSize int64
	contentHasher := sha256.New()
	for _, filePath := range paths {
		file := files[filePath]
		sum := sha256.Sum256(file.Data)
		hash := hex.EncodeToString(sum[:])
		size := int64(len(file.Data))
		manifestFiles = append(manifestFiles, ManifestFile{Path: filePath, Size: size, SHA256: hash, Executable: file.Executable})
		contentSize += size
		if contentSize > maxManifestContentBytes {
			return Prepared{}, errors.New("cloud resource package exceeds the content limit")
		}
		executable := "0"
		if file.Executable {
			executable = "1"
		}
		_, _ = contentHasher.Write([]byte(filePath + "\x00" + strconv.FormatInt(size, 10) + "\x00" + hash + "\x00" + executable + "\n"))
	}
	return Prepared{
		Manifest: Manifest{
			FormatSchema:         FormatSchemaV2,
			ResourceType:         resourceType,
			ResourceName:         resourceName,
			ClientResourceKey:    clientResourceKey,
			Entrypoint:           entrypoint,
			Files:                manifestFiles,
			ContentSize:          contentSize,
			ContentHash:          hex.EncodeToString(contentHasher.Sum(nil)),
			TransportFormat:      "zip",
			MinDesktopVersion:    desktopVersion,
			RequiredCapabilities: []string{},
			Permissions:          []string{},
		},
		Files: files,
	}, nil
}

func validResourceName(value string) bool {
	return value != "" && len(value) <= 255 && value != "." && value != ".." && !strings.ContainsAny(value, `/\\`)
}

func validClientResourceKey(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if char < '!' || char > '~' {
			return false
		}
	}
	return true
}

func validDesktopVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		if _, err := strconv.ParseUint(part, 10, 32); err != nil {
			return false
		}
	}
	return true
}

func packageShape(resourceType string) (string, []string, error) {
	switch resourceType {
	case "skill":
		return "SKILL.md", []string{"SKILL.md"}, nil
	case "workflow":
		return "workflow.yaml", []string{"workflow.yaml", "scenario/state.yml", "scenario/scenario.md"}, nil
	default:
		return "", nil, fmt.Errorf("unsupported cloud resource type %q", resourceType)
	}
}

func cleanPath(value string) (string, error) {
	if value == "" || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") {
		return "", errors.New("invalid path")
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("invalid path")
	}
	return clean, nil
}
