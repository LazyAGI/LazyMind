package agentexec

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ElectronVersion reads the version in an installed desktop Electron package.
func ElectronVersion(application string) (string, error) {
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
