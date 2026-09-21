package cloudpackage

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

type ZIPLimits struct {
	MaxArchiveBytes int64
	MaxFiles        int
	MaxFileBytes    int64
	MaxContentBytes int64
}

type VerifyZIPInput struct {
	ZIPPath             string
	ResourceType        string
	ResourceName        string
	ClientResourceKey   string
	DesktopVersion      string
	ExpectedContentHash string
	ExpectedContentSize int64
	Limits              ZIPLimits
}

func ReadAndVerifyZIP(input VerifyZIPInput) (Prepared, error) {
	files, err := ReadZIP(input.ZIPPath, input.ResourceType, input.Limits)
	if err != nil {
		return Prepared{}, err
	}
	prepared, err := Prepare(PrepareInput{
		ResourceType: input.ResourceType, ResourceName: input.ResourceName,
		ClientResourceKey: input.ClientResourceKey, DesktopVersion: input.DesktopVersion, Files: files,
	})
	if err != nil {
		return Prepared{}, err
	}
	if input.ExpectedContentSize >= 0 && prepared.Manifest.ContentSize != input.ExpectedContentSize {
		return Prepared{}, errors.New("cloud resource content size mismatch")
	}
	if expected := strings.TrimSpace(input.ExpectedContentHash); expected == "" || prepared.Manifest.ContentHash != expected {
		return Prepared{}, errors.New("cloud resource content hash mismatch")
	}
	return prepared, nil
}

func (limits ZIPLimits) defaults() ZIPLimits {
	if limits.MaxArchiveBytes <= 0 {
		limits.MaxArchiveBytes = 100 << 20
	}
	if limits.MaxFiles <= 0 {
		limits.MaxFiles = 10_000
	}
	if limits.MaxFileBytes <= 0 {
		limits.MaxFileBytes = 100 << 20
	}
	if limits.MaxContentBytes <= 0 {
		limits.MaxContentBytes = 500 << 20
	}
	return limits
}

func ReadZIP(zipPath, resourceType string, limits ZIPLimits) (map[string]File, error) {
	limits = limits.defaults()
	info, err := os.Stat(zipPath)
	if err != nil {
		return nil, err
	}
	if info.Size() > limits.MaxArchiveBytes {
		return nil, errors.New("cloud resource ZIP exceeds the archive limit")
	}
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	files := make(map[string]File)
	var contentSize int64
	for _, entry := range reader.File {
		name := strings.TrimSuffix(entry.Name, "/")
		clean, pathErr := cleanPath(name)
		if pathErr != nil || clean != name {
			return nil, fmt.Errorf("unsafe cloud resource ZIP path %q", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		mode := entry.Mode()
		if !mode.IsRegular() {
			return nil, fmt.Errorf("cloud resource ZIP contains a non-regular file: %s", name)
		}
		if len(files) >= limits.MaxFiles {
			return nil, errors.New("cloud resource ZIP contains too many files")
		}
		if _, duplicate := files[name]; duplicate {
			return nil, fmt.Errorf("cloud resource ZIP contains duplicate path %q", name)
		}
		if entry.UncompressedSize64 > uint64(limits.MaxFileBytes) {
			return nil, fmt.Errorf("cloud resource ZIP file exceeds the limit: %s", name)
		}
		contentSize += int64(entry.UncompressedSize64)
		if contentSize > limits.MaxContentBytes {
			return nil, errors.New("cloud resource ZIP content exceeds the limit")
		}
		stream, err := entry.Open()
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(stream, limits.MaxFileBytes+1))
		closeErr := stream.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if int64(len(body)) > limits.MaxFileBytes || int64(len(body)) != int64(entry.UncompressedSize64) {
			return nil, fmt.Errorf("cloud resource ZIP file size is invalid: %s", name)
		}
		executable := mode.Perm()&0o111 != 0
		if executable && !strings.HasPrefix(name, "scripts/") {
			return nil, fmt.Errorf("cloud resource ZIP executable is outside scripts: %s", name)
		}
		files[name] = File{Data: body, Executable: executable}
	}
	_, required, err := packageShape(strings.TrimSpace(resourceType))
	if err != nil {
		return nil, err
	}
	for _, path := range required {
		if _, exists := files[path]; !exists {
			return nil, fmt.Errorf("cloud %s package must contain %s", resourceType, path)
		}
	}
	return files, nil
}
