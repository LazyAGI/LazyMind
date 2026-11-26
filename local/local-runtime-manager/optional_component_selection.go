package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

// A manual first launch has no native transaction. Read local evidence only
// to establish intent: executable targets always come from the newly verified
// installed app. A stale directory or a version marker alone is insufficient.
func migrateManualComponentSelection(paths RuntimePaths, identity desktopupdate.Config) error {
	state, err := readRuntimeState(paths.StateFile)
	known := err == nil && state.Profile == "desktop" && sameRuntimePath(state.RuntimeRoot, paths.RuntimeRoot) && sameRuntimePath(state.ResourcesRoot, paths.ResourcesRoot)
	raw, err := os.ReadFile(filepath.Join(paths.ResourcesRoot, "config", "python-components.json"))
	if err != nil {
		return nil
	}
	catalog, err := resolvePythonComponentCatalog(paths, raw, false)
	if err != nil {
		return nil
	}
	lock, err := desktopupdate.NativeComponentLock(paths.RuntimeRoot)
	if err != nil {
		return nil // An explicit install/uninstall owns this decision.
	}
	defer desktopupdate.ReleaseNativeLock(lock)
	if prior, e := desktopupdate.ReadSelectedComponents(paths.RuntimeRoot); e != nil || prior != nil {
		return nil
	}
	var wanted []string
	revisions := map[string]string{}
	needsReplacement := false
	for _, id := range pythonComponentIDs {
		if !known || id == "rag" && state.Config.Algorithm.RAGDisabled {
			continue
		}
		revision := previousComponentRevision(paths, id)
		if revision == "" {
			continue
		}
		wanted = append(wanted, id)
		revisions[id] = revision
		pin := catalog.Components[id]
		if pin.Revision != revision || installedPythonComponentPath(paths, pin) == "" {
			needsReplacement = true
		}
	}
	// An unchanged manual pin/compatible online selection remains usable
	// through the normal path. An OTA activation always requires a target probe.
	if !needsReplacement {
		return nil
	}
	tx, e := randomHexToken()
	if e != nil {
		return e
	}
	digest := sha256.Sum256(raw)
	return desktopupdate.SaveSelectedComponents(paths.RuntimeRoot, desktopupdate.SelectedComponents{ReleaseID: identity.ReleaseID, ComponentsSHA256: hex.EncodeToString(digest[:]), TransactionID: tx[:32], Wanted: wanted, SourceRevisions: revisions})
}

func previousComponentRevision(paths RuntimePaths, id string) string {
	base := filepath.Join(paths.RuntimeRoot, "deps", "python-components", id)
	files, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	valid := map[string]bool{}
	for _, f := range files {
		if f.Type()&os.ModeSymlink != 0 {
			continue
		}
		if !f.IsDir() && strings.HasPrefix(f.Name(), "catalog-") && strings.HasSuffix(f.Name(), ".json") {
			raw, e := os.ReadFile(filepath.Join(base, f.Name()))
			var d pythonComponent
			if e != nil || json.Unmarshal(raw, &d) != nil || d.ID != id || d.Platform != runtime.GOOS || d.Arch != runtime.GOARCH || !componentDigestPattern.MatchString(d.Tag) || f.Name() != "catalog-"+d.Tag+".json" ||
				!componentDigestPattern.MatchString(d.Revision) || !componentDigestPattern.MatchString(d.SHA256) || installedPythonComponentPath(paths, d) == "" {
				return ""
			}
			valid[d.Revision] = true
		}
	}
	if len(valid) > 0 {
		if len(valid) == 1 {
			for revision := range valid {
				return revision
			}
		}
		return ""
	}
	// Older packaged pins can lack a catalog receipt. Admit only one complete
	// revision, with a valid manifest, ready hash and a real site-packages tree.
	for _, f := range files {
		if !f.IsDir() || !componentDigestPattern.MatchString(f.Name()) {
			continue
		}
		dir := filepath.Join(base, f.Name())
		raw, e := os.ReadFile(filepath.Join(dir, "bundle-manifest.json"))
		var d pythonComponent
		if e != nil || json.Unmarshal(raw, &d) != nil || d.ID != id || d.Platform != runtime.GOOS || d.Arch != runtime.GOARCH || d.Revision != f.Name() || !componentDigestPattern.MatchString(d.BaseFingerprint) || d.PythonABI == "" || len(d.Packages) == 0 {
			continue
		}
		ready, e := os.ReadFile(filepath.Join(dir, ".ready"))
		if e != nil {
			continue
		}
		digest := strings.TrimSpace(string(ready))
		if !componentDigestPattern.MatchString(digest) || (d.SHA256 != "" && d.SHA256 != digest) {
			continue
		}
		d.SHA256 = digest
		if installedPythonComponentPath(paths, d) != "" {
			valid[d.Revision] = true
		}
	}
	if len(valid) == 1 {
		for revision := range valid {
			return revision
		}
	}
	return ""
}
