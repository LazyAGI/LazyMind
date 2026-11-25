package workflowconnector

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"lazymind/agentconnector/internal/localfile"
)

const maxDSHPluginFileSize = 8 << 20
const dshPluginName = "@lazymind/dsh-workflow"

var dshBuildPattern = regexp.MustCompile(`^[0-9][A-Za-z0-9.-]{0,63}\+[a-f0-9]{16}$`)

func ValidDSHBuild(build string) bool { return dshBuildPattern.MatchString(build) }

var dshFiles = []string{"package.json", "lib/index.js", "lib/client.js", "cordis.patch.yml", "README.md"}

// DSHRelease opens an explicitly selected release or retains a verified
// package alongside the other workflow connectors. Profile selection belongs
// to the DSH installer and is independent of updates to the distribution.
func DSHRelease(ctx context.Context, home, executable, directory, release string) (Installation, error) {
	if release != "" {
		if !digestPattern.MatchString(release) {
			return Installation{}, errors.New("invalid DSH Connector release")
		}
		dir := filepath.Join(filepath.Dir(RecordPath(home, DSHHost)), "connectors", release)
		manifest, err := readManifest(filepath.Join(dir, manifestName), DSHHost)
		if err != nil {
			return Installation{}, err
		}
		if manifest.Release != release {
			return Installation{}, errors.New("retained DSH Connector manifest does not identify the requested release")
		}
		value := Installation{Manifest: manifest, Dir: dir}
		return value, value.verifyRelease("")
	}
	value, err := packaged(executable, DSHHost)
	if directory != "" {
		manifest, manifestErr := readManifest(filepath.Join(directory, manifestName), DSHHost)
		value, err = Installation{Manifest: manifest, Dir: directory}, manifestErr
	} else if errors.Is(err, os.ErrNotExist) {
		return Resolve(home, DSHHost)
	}
	if err != nil {
		return Installation{}, err
	}
	if err := value.verifyRelease(""); err != nil {
		return Installation{}, err
	}
	record := RecordPath(home, DSHHost)
	if err := os.MkdirAll(filepath.Dir(record), 0700); err != nil {
		return Installation{}, err
	}
	unlock, err := localfile.LockContext(ctx, record+".lock")
	if err != nil {
		return Installation{}, err
	}
	defer unlock()
	return value.retain(home)
}

// CheckDSHInstall verifies the native install using its package files. The
// running build is independently reported by the loaded plugin.
func (i Installation) CheckDSHInstall(directory string) error {
	body, err := os.ReadFile(i.EntryPath())
	if err != nil {
		return err
	}
	files, err := dshPluginFiles(body, i.Version)
	if err != nil {
		return err
	}
	for _, name := range dshFiles[:len(dshFiles)-1] {
		installed, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		if !bytes.Equal(installed, files[name]) {
			return fmt.Errorf("installed DSH Connector %s does not match build %s", name, i.Version)
		}
	}
	return nil
}

func dshPluginFiles(data []byte, build string) (map[string][]byte, error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	files := make(map[string][]byte, len(dshFiles))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		name, ok := strings.CutPrefix(header.Name, "package/")
		if !ok || !isDSHPluginFile(name) || files[name] != nil || header.Typeflag != tar.TypeReg || header.Size > maxDSHPluginFileSize {
			return nil, fmt.Errorf("invalid DSH Connector archive member %q", header.Name)
		}
		files[name], err = io.ReadAll(reader)
		if err != nil {
			return nil, err
		}
	}
	for _, name := range dshFiles[:len(dshFiles)-1] {
		if len(files[name]) == 0 {
			return nil, fmt.Errorf("DSH Connector archive is missing %s", name)
		}
	}
	var manifest struct {
		Name    string            `json:"name"`
		Version string            `json:"version"`
		Exports map[string]string `json:"exports"`
		DSH     struct {
			Bundle struct {
				Patch string `json:"patch"`
			} `json:"bundle"`
			Client struct {
				Platform string `json:"platform"`
			} `json:"client"`
		} `json:"dsh"`
	}
	version, _, _ := strings.Cut(build, "+")
	if json.Unmarshal(files["package.json"], &manifest) != nil || manifest.Name != dshPluginName ||
		!ValidDSHBuild(build) || manifest.Version != version || manifest.Exports["."] != "./lib/index.js" ||
		manifest.Exports["./client"] != "./lib/client.js" || manifest.DSH.Bundle.Patch != "./cordis.patch.yml" || manifest.DSH.Client.Platform != "web" {
		return nil, errors.New("invalid DSH Connector package metadata")
	}
	for _, name := range []string{"lib/index.js", "lib/client.js"} {
		if !bytes.Contains(files[name], []byte(`"`+build+`"`)) {
			return nil, fmt.Errorf("DSH Connector %s is missing build %s", name, build)
		}
	}
	return files, nil
}

func isDSHPluginFile(name string) bool {
	for _, file := range dshFiles {
		if file == name {
			return true
		}
	}
	return false
}
