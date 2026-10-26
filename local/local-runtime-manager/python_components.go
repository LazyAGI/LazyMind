package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
)

type pythonComponent struct {
	ID              string            `json:"id"`
	Revision        string            `json:"revision"`
	SHA256          string            `json:"sha256"`
	BaseFingerprint string            `json:"baseFingerprint"`
	Platform        string            `json:"platform"`
	Arch            string            `json:"arch"`
	PythonABI       string            `json:"pythonAbi"`
	Packages        map[string]string `json:"packages"`
	CatalogURL      string            `json:"catalogUrl,omitempty"`
	Tag             string            `json:"tag,omitempty"`
}

type pythonComponentCatalog struct {
	SchemaVersion int                        `json:"schemaVersion"`
	Platform      string                     `json:"platform"`
	Arch          string                     `json:"arch"`
	Components    map[string]pythonComponent `json:"components"`
}

var componentDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func sourceRAGInstalled(paths RuntimePaths) bool {
	data, err := os.ReadFile(filepath.Join(paths.RuntimeRoot, "deps", "python-components", "rag", "source.ready"))
	return err == nil && strings.TrimSpace(string(data)) == paths.AlgorithmPython
}

func loadPythonComponentCatalog(paths RuntimePaths) (*pythonComponentCatalog, error) {
	data, err := os.ReadFile(filepath.Join(paths.ResourcesRoot, "config", "python-components.json"))
	if os.IsNotExist(err) {
		return nil, nil // Older full bundles and source development.
	}
	if err != nil {
		return nil, err
	}
	var catalog pythonComponentCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("read Python component catalog: %w", err)
	}
	if catalog.SchemaVersion != 1 || catalog.Platform != runtime.GOOS || catalog.Arch != runtime.GOARCH {
		return nil, fmt.Errorf("Python component catalog does not match %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	for _, id := range []string{"rag"} {
		entry, ok := catalog.Components[id]
		if !ok || entry.ID != id || !componentDigestPattern.MatchString(entry.Revision) ||
			!componentDigestPattern.MatchString(entry.SHA256) || !componentDigestPattern.MatchString(entry.BaseFingerprint) {
			return nil, fmt.Errorf("invalid Python component descriptor: %s", id)
		}
		if entry.CatalogURL != "" {
			if !componentDigestPattern.MatchString(entry.Tag) {
				return nil, fmt.Errorf("invalid Python component tag")
			}
			data, err := os.ReadFile(filepath.Join(paths.RuntimeRoot, "deps", "python-components", id, "catalog-"+entry.Tag+".json"))
			var selected pythonComponent
			if err == nil && json.Unmarshal(data, &selected) == nil &&
				selected.ID == entry.ID && selected.Tag == entry.Tag && selected.CatalogURL == entry.CatalogURL &&
				selected.Platform == entry.Platform && selected.Arch == entry.Arch && selected.PythonABI == entry.PythonABI &&
				selected.BaseFingerprint == entry.BaseFingerprint && reflect.DeepEqual(selected.Packages, entry.Packages) &&
				componentDigestPattern.MatchString(selected.Revision) && componentDigestPattern.MatchString(selected.SHA256) &&
				installedPythonComponentPath(paths, selected) != "" {
				catalog.Components[id] = selected
			}
		}
	}
	return &catalog, nil
}

func installedPythonComponentPath(paths RuntimePaths, entry pythonComponent) string {
	root := filepath.Join(paths.RuntimeRoot, "deps", "python-components", entry.ID, entry.Revision)
	marker, err := os.ReadFile(filepath.Join(root, ".ready"))
	if err != nil || strings.TrimSpace(string(marker)) != entry.SHA256 {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(root, "bundle-manifest.json"))
	var installed pythonComponent
	if err != nil || json.Unmarshal(data, &installed) != nil || installed.ID != entry.ID ||
		installed.Revision != entry.Revision || installed.BaseFingerprint != entry.BaseFingerprint ||
		installed.Platform != entry.Platform || installed.Arch != entry.Arch || installed.PythonABI != entry.PythonABI ||
		!reflect.DeepEqual(installed.Packages, entry.Packages) {
		return ""
	}
	site := filepath.Join(root, "site-packages")
	if info, err := os.Stat(site); err != nil || !info.IsDir() {
		return ""
	}
	return site
}

func pythonComponentEnvironment(paths RuntimePaths, ragDisabled bool) []string {
	values := []string{
		"LAZYMIND_PYTHON_COMPONENT_CATALOG=" + filepath.Join(paths.ResourcesRoot, "config", "python-components.json"),
		"LAZYMIND_ALGORITHM_PYTHON=" + paths.AlgorithmPython,
		"LAZYMIND_ALGORITHM_SOURCE=" + filepath.Join(paths.RepoRoot, "algorithm"),
	}
	catalog, err := loadPythonComponentCatalog(paths)
	if err != nil || catalog == nil {
		if ragDisabled {
			values = append(values, ragEnabledEnvVar+"=0")
		} else {
			values = append(values, ragEnabledEnvVar+"=1")
		}
		if paths.RepoRoot != "" {
			values = append(values, "LAZYMIND_SOURCE_COMPONENT_INSTALLER="+filepath.Join(paths.BuildRoot, "bin", executableName("local-runtime-manager")))
		}
		return values
	}
	var sites []string
	for _, id := range []string{"rag"} {
		enabled := "0"
		if site := installedPythonComponentPath(paths, catalog.Components[id]); site != "" && !ragDisabled {
			sites = append(sites, site)
			enabled = "1"
		}
		values = append(values, "LAZYMIND_"+strings.ToUpper(id)+"_ENABLED="+enabled)
	}
	return append(values, "LAZYMIND_PYTHON_COMPONENT_PATHS="+strings.Join(sites, string(os.PathListSeparator)))
}

func pythonComponentSites(paths RuntimePaths) []string {
	catalog, err := loadPythonComponentCatalog(paths)
	if err != nil || catalog == nil {
		return nil
	}
	var sites []string
	for _, id := range []string{"rag"} {
		if site := installedPythonComponentPath(paths, catalog.Components[id]); site != "" {
			sites = append(sites, site)
		}
	}
	return sites
}
