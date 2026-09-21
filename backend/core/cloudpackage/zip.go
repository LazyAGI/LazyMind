package cloudpackage

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"time"
)

var deterministicZIPTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

func WriteZIP(prepared Prepared) (Prepared, error) {
	file, err := os.CreateTemp("", "lazymind-cloud-resource-*.zip")
	if err != nil {
		return Prepared{}, err
	}
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}
	writer := zip.NewWriter(file)
	for _, item := range prepared.Manifest.Files {
		header := &zip.FileHeader{Name: item.Path, Method: zip.Deflate}
		header.SetModTime(deterministicZIPTime)
		if item.Executable {
			header.SetMode(0o755)
		} else {
			header.SetMode(0o644)
		}
		entry, createErr := writer.CreateHeader(header)
		if createErr != nil {
			cleanup()
			return Prepared{}, createErr
		}
		if _, writeErr := entry.Write(prepared.Files[item.Path].Data); writeErr != nil {
			cleanup()
			return Prepared{}, writeErr
		}
	}
	if err := writer.Close(); err != nil {
		cleanup()
		return Prepared{}, err
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return Prepared{}, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return Prepared{}, err
	}
	hasher := sha256.New()
	size, err := io.Copy(hasher, file)
	if err != nil {
		cleanup()
		return Prepared{}, err
	}
	if err := file.Chmod(0o600); err != nil {
		cleanup()
		return Prepared{}, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return Prepared{}, err
	}
	prepared.Manifest.TransportSize = size
	prepared.Manifest.TransportHash = hex.EncodeToString(hasher.Sum(nil))
	prepared.ZIPPath = file.Name()
	return prepared, nil
}
