package systemdeps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// The tag identifies a compatible release channel; revision identifies its
// current signed payload. Re-signing can change revision without changing tag.
func compatiblePythonDescriptor(pinned, resolved PythonComponent) bool {
	return resolved.ID == pinned.ID && resolved.Platform == pinned.Platform &&
		resolved.Arch == pinned.Arch && resolved.PythonABI == pinned.PythonABI &&
		resolved.BaseFingerprint == pinned.BaseFingerprint && reflect.DeepEqual(resolved.Packages, pinned.Packages) &&
		pythonComponentDigest.MatchString(resolved.Revision) && pythonComponentDigest.MatchString(resolved.SHA256) &&
		resolved.SizeBytes > 0 && resolved.UnpackedBytes > 0 &&
		resolved.Filename != "" && resolved.Filename != "." && resolved.Filename != ".." &&
		filepath.Base(resolved.Filename) == resolved.Filename &&
		!strings.ContainsAny(resolved.Filename, `\:`) &&
		validateAssetMirrors(resolved.URL, resolved.FallbackURL, resolved.Filename) == nil
}

func resolvePythonDescriptor(ctx context.Context, client *http.Client, pinned PythonComponent) (PythonComponent, error) {
	if err := validateAssetURL(pinned.CatalogURL); err != nil {
		return pinned, err
	}
	secure := *client
	secure.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many catalog redirects")
		}
		return validateAssetURL(req.URL.String())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pinned.CatalogURL, nil)
	if err != nil {
		return pinned, err
	}
	res, err := secure.Do(req)
	if err != nil {
		return pinned, fmt.Errorf("download RAG catalog: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return pinned, fmt.Errorf("RAG catalog returned HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 1024*1024+1))
	if err != nil {
		return pinned, err
	}
	if len(data) > 1024*1024 {
		return pinned, errors.New("RAG catalog exceeds 1 MiB")
	}
	var catalog pythonCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return pinned, err
	}
	resolved := catalog.Components[pinned.ID]
	if catalog.SchemaVersion != 1 || catalog.Platform != pinned.Platform || catalog.Arch != pinned.Arch ||
		(resolved.Tag != "" && resolved.Tag != pinned.Tag) || !compatiblePythonDescriptor(pinned, resolved) {
		return pinned, errors.New("online RAG catalog is incompatible with this client")
	}
	// Never let remote metadata redirect the next catalog refresh to another channel.
	resolved.Tag, resolved.CatalogURL = pinned.Tag, pinned.CatalogURL
	return resolved, nil
}

func pythonDescriptorPath(root string, entry PythonComponent) string {
	return filepath.Join(root, "deps", "python-components", entry.ID, "catalog-"+entry.Tag+".json")
}

func installedPythonDescriptor(root string, pinned PythonComponent) PythonComponent {
	if pinned.CatalogURL == "" || !pythonComponentDigest.MatchString(pinned.Tag) {
		return pinned
	}
	data, err := os.ReadFile(pythonDescriptorPath(root, pinned))
	var resolved PythonComponent
	if err != nil || json.Unmarshal(data, &resolved) != nil || resolved.Tag != pinned.Tag ||
		resolved.CatalogURL != pinned.CatalogURL || !compatiblePythonDescriptor(pinned, resolved) ||
		!pythonComponentInstalled(root, resolved) {
		return pinned
	}
	return resolved
}

func savePythonDescriptor(root string, entry PythonComponent) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	target := pythonDescriptorPath(root, entry)
	f, err := os.CreateTemp(filepath.Dir(target), ".catalog-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), target)
}
