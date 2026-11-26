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
)

const maxDSHPluginFileSize = 8 << 20
const dshPluginName = "@lazymind/dsh-workflow"

var dshBuildPattern = regexp.MustCompile(`^[0-9][A-Za-z0-9.-]{0,63}\+[a-f0-9]{16}$`)

func ValidDSHBuild(build string) bool { return dshBuildPattern.MatchString(build) }

var dshFiles = []string{"package.json", "lib/index.js", "lib/client.js", "cordis.patch.yml", "README.md"}

// DSHRelease resolves the selected archive for the native installer and
// provisions it on the first connection from a distribution.
func DSHRelease(ctx context.Context, home, executable string) (Installation, error) {
	distributed, err := hasPackage(executable)
	if err != nil {
		return Installation{}, err
	}
	if !distributed {
		selected, err := Resolve(home, DSHHost)
		if selected.Release != "" || !errors.Is(err, os.ErrNotExist) {
			return selected, err
		}
		return selectRelease(ctx, home, DSHHost, nil, false)
	}
	// A distribution must supply its DSH member, even if the selected
	// release is retained from an earlier distribution.
	value, err := packaged(executable, DSHHost)
	if err != nil {
		return Installation{}, err
	}
	if err := value.verifyRelease(""); err != nil {
		return Installation{}, err
	}
	return selectRelease(ctx, home, DSHHost, &value, false)
}

func retainedDSHRelease(home, release string) (Installation, error) {
	if !digestPattern.MatchString(release) {
		return Installation{}, errors.New("invalid installed DSH Connector release")
	}
	dir := filepath.Join(filepath.Dir(RecordPath(home, DSHHost)), "connectors", release)
	path := filepath.Join(dir, manifestName)
	manifest, err := readManifest(path, DSHHost)
	if err != nil {
		return Installation{}, err
	}
	if manifest.Release != release {
		return Installation{}, fmt.Errorf("retained DSH Connector release does not match its directory at %s", path)
	}
	return Installation{Manifest: manifest, Dir: dir}, nil
}

func (i Installation) verifyRetainedDSHRelease(home string) error {
	retained, err := retainedDSHRelease(home, i.Release)
	if err != nil {
		return err
	}
	if retained.Version != i.Version {
		return fmt.Errorf("retained DSH Connector version does not match the installation record at %s", retained.Dir)
	}
	return retained.verifyRelease("")
}

// Earlier connects retained the archive without writing an active record.
// Recover an unambiguous release while holding the installation record lock.
func recoverDSHSelection(home string) (Installation, error) {
	dir := filepath.Join(filepath.Dir(RecordPath(home, DSHHost)), "connectors")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return Installation{}, nil
	}
	if err != nil {
		return Installation{}, err
	}
	release := ""
	for _, entry := range entries {
		if !digestPattern.MatchString(entry.Name()) {
			continue
		}
		if release != "" {
			return Installation{}, fmt.Errorf("multiple retained DSH Connector releases at %s; deploy LazyMind again to select a release", dir)
		}
		release = entry.Name()
	}
	if release == "" {
		return Installation{}, nil
	}
	selected, err := retainedDSHRelease(home, release)
	if err != nil {
		return Installation{}, err
	}
	return selected, selected.verifyRelease("")
}

// CheckRetainedDSHInstall compares a native installation with the archive
// that supplied it. Connect uses the currently selected archive.
func CheckRetainedDSHInstall(home, release, build, directory string) error {
	value, err := retainedDSHRelease(home, release)
	if err != nil {
		return err
	}
	if value.Version != build {
		return errors.New("retained DSH Connector does not match the installed build")
	}
	if err := value.verifyRelease(""); err != nil {
		return err
	}
	return value.CheckDSHInstall(directory)
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
