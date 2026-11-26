package systemdeps

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

type replacementPython struct{ Exe, ABI, Base, Module, Image, Suffix string }

func replacementInterpreter(t *testing.T) (replacementPython, replacementPython) {
	t.Helper()
	var interpreters []replacementPython
	for _, name := range []string{"python3.11", "python3", "python3.12", "python3.10", "python3.9", "python3.13", "/usr/bin/python3", "python"} {
		exe, e := exec.LookPath(name)
		if e != nil {
			continue
		}
		const probe = `import importlib, importlib.machinery as loaders, json, pathlib, sys
for name in ('_csv', '_decimal', '_bz2', 'select', '_hashlib'):
    mod=importlib.import_module(name)
    path=getattr(mod,'__file__',None)
    if path and pathlib.Path(path).is_file() and any(path.endswith(s) for s in loaders.EXTENSION_SUFFIXES):
        print(json.dumps(dict(exe=sys.executable,abi=f'cp{sys.version_info.major}{sys.version_info.minor}',module=name,image=path,suffix=loaders.EXTENSION_SUFFIXES[0])))
        break`
		out, e := exec.CommandContext(t.Context(), exe, "-I", "-B", "-c", probe).Output()
		var found replacementPython
		if e != nil || json.Unmarshal(out, &found) != nil || found.Image == "" {
			continue
		}
		if len(interpreters) != 0 && interpreters[0].ABI == found.ABI {
			continue
		}
		interpreters = append(interpreters, found)
		if len(interpreters) == 2 {
			break
		}
	}
	if len(interpreters) == 0 {
		t.Skip("no CPython interpreter with an importable native extension")
	}
	// Use a fresh target with no system wheel dependencies.
	target := interpreters[0]
	dir := filepath.Join(t.TempDir(), "target-venv")
	if out, e := exec.CommandContext(t.Context(), target.Exe, "-m", "venv", "--without-pip", dir).CombinedOutput(); e != nil {
		t.Fatalf("target Python venv: %v %s", e, out)
	}
	target.Exe = filepath.Join(dir, "bin", "python")
	if runtime.GOOS == "windows" {
		target.Exe = filepath.Join(dir, "Scripts", "python.exe")
	}
	identity := map[string]string{"platform": runtime.GOOS, "arch": runtime.GOARCH, "pythonAbi": target.ABI}
	base := map[string]any{"identity": map[string]any{"schemaVersion": 1, "platform": identity["platform"], "arch": identity["arch"], "pythonAbi": target.ABI}, "records": map[string]string{}, "versions": map[string]string{}}
	encoded := append(fixtureJSON(t, base), '\n')
	hash := sha256.Sum256(encoded)
	target.Base = hex.EncodeToString(hash[:])
	if len(interpreters) == 1 {
		return replacementPython{}, target
	}
	return interpreters[1], target
}

func replacementPayload(t *testing.T, id, release string, py replacementPython) (PythonComponent, []byte) {
	t.Helper()
	digest := func(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
	component := PythonComponent{ID: id, Revision: digest([]byte(release + id + py.ABI)), Tag: digest([]byte(release + "-tag-" + id)), BaseFingerprint: py.Base, Platform: runtime.GOOS, Arch: runtime.GOARCH, PythonABI: py.ABI, Filename: id + ".zip", Packages: map[string]string{"fixture-" + id: "1"}}
	if component.BaseFingerprint == "" {
		component.BaseFingerprint = digest([]byte("source-" + py.ABI))
	}
	component.URL = "https://huggingface.co/fixture/resolve/main/" + component.Revision + "/" + id + ".zip"
	component.CatalogURL = "https://huggingface.co/fixture/resolve/main/" + component.Tag + "/catalog.json"
	image, e := os.ReadFile(py.Image)
	if e != nil {
		t.Fatal(e)
	}
	pkg := "inplace_" + id
	files := map[string][]byte{"bundle-manifest.json": fixtureJSON(t, component), "site-packages/" + pkg + "/__init__.py": {}, "site-packages/" + pkg + "/" + py.Module + py.Suffix: image,
		"site-packages/fixture_" + id + "-1.dist-info/METADATA": []byte("Name: fixture-" + id + "\nVersion: 1\n"), "site-packages/fixture_" + id + "-1.dist-info/RECORD": []byte("fixture\n")}
	for _, name := range pythonComponentRuleFor(id).modules {
		file := "site-packages/" + strings.ReplaceAll(name, ".", "/") + "/__init__.py"
		files[file] = []byte(fmt.Sprintf("from %s import %s as native\nBUILD = %q\n", pkg, py.Module, release))
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for name, data := range files {
		entry, e := writer.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e := entry.Write(data); e != nil {
			t.Fatal(e)
		}
		component.UnpackedBytes += int64(len(data))
	}
	if e := writer.Close(); e != nil {
		t.Fatal(e)
	}
	component.SHA256, component.SizeBytes = digest(archive.Bytes()), int64(archive.Len())
	return component, archive.Bytes()
}

func checkReplacementImport(t *testing.T, py replacementPython, site, id, build string, want bool) {
	t.Helper()
	module := "pymilvus"
	if id == "cluster" {
		module = "umap"
	}
	const code = `import importlib,pathlib,sys
sys.path.insert(0,sys.argv[1])
mod=importlib.import_module(sys.argv[2])
assert mod.BUILD==sys.argv[3]
assert pathlib.Path(mod.native.__file__).resolve().is_relative_to(pathlib.Path(sys.argv[1]).resolve())`
	out, e := exec.CommandContext(t.Context(), py.Exe, "-I", "-B", "-c", code, site, module, build).CombinedOutput()
	if (e == nil) != want {
		t.Fatalf("%s %s actual native import: %v %s", py.ABI, site, e, out)
	}
}

func TestInPlaceNativeOptionalReplacementUsesTargetPython(t *testing.T) {
	old, newPython := replacementInterpreter(t)
	for _, tc := range []struct {
		name     string
		ids      []string
		crossABI bool
	}{{"same ABI new release", []string{"rag", "cluster"}, false}, {"RAG different ABI", []string{"rag"}, true}, {"cluster different ABI", []string{"cluster"}, true}, {"both different ABI", []string{"rag", "cluster"}, true}} {
		t.Run(tc.name, func(t *testing.T) {
			source := newPython
			if tc.crossABI {
				if old.Exe == "" {
					t.Skip("second real CPython ABI not installed")
				}
				source = old
			}
			f := newMaintenanceFixture(t, false)
			if e := os.RemoveAll(filepath.Join(f.runtimeRoot, "deps", "python-components", "rag")); e != nil {
				t.Fatal(e)
			}
			oldEntries := map[string]PythonComponent{}
			targets := map[string]PythonComponent{}
			archives := map[string][]byte{}
			updates := map[string]map[string]PythonComponent{}
			for _, id := range []string{"rag", "cluster"} {
				next, zipB := replacementPayload(t, id, "B", newPython)
				targets[id] = next
				archives[next.URL] = zipB
				if !slices.Contains(tc.ids, id) {
					continue
				}
				before, zipA := replacementPayload(t, id, "A", source)
				oldEntries[id] = before
				updates[id] = map[string]PythonComponent{"source": before, "target": next}
				file := filepath.Join(t.TempDir(), "A.zip")
				writeMaintenanceFile(t, file, zipA)
				dir := pythonComponentDir(f.runtimeRoot, before)
				if e := os.MkdirAll(dir, 0700); e != nil {
					t.Fatal(e)
				}
				if e := extractPythonComponent(t.Context(), file, dir, before.UnpackedBytes); e != nil {
					t.Fatal(e)
				}
				writeMaintenanceFile(t, filepath.Join(dir, ".ready"), []byte(before.SHA256))
				if e := savePythonDescriptor(f.runtimeRoot, before); e != nil {
					t.Fatal(e)
				}
				if e := readMaintenanceSelection(f.runtimeRoot, before); e != nil {
					t.Fatal(e)
				}
				site := filepath.Join(dir, "site-packages")
				checkReplacementImport(t, source, site, id, "A", true)
				if tc.crossABI {
					checkReplacementImport(t, newPython, site, id, "A", false)
				}
			}
			body := fixtureJSON(t, map[string]any{"schemaVersion": 1, "platform": runtime.GOOS, "arch": runtime.GOARCH, "runtimeBaseFingerprint": newPython.Base, "components": targets})
			writeMaintenanceFile(t, f.record["componentsPath"].(string), body)
			hash := sha256.Sum256(body)
			f.record["componentsSHA256"], f.record["componentsSize"] = hex.EncodeToString(hash[:]), len(body)
			delete(f.record, "sourceComponent")
			delete(f.record, "targetComponent")
			f.record["componentUpdates"] = updates
			f.record["sourceHadRAG"] = slices.Contains(tc.ids, "rag")
			if !slices.Contains(tc.ids, "rag") {
				f.record["componentMode"] = "absent"
			}
			f.save(t)
			requests := map[string]int{}
			f.client = &http.Client{Transport: maintenanceHTTP(func(req *http.Request) (*http.Response, error) {
				payload, ok := archives[req.URL.String()]
				if !ok {
					return nil, fmt.Errorf("unbound component download")
				}
				requests[req.URL.String()]++
				return &http.Response{StatusCode: 200, Header: make(http.Header), Request: req, Body: io.NopCloser(bytes.NewReader(payload))}, nil
			})}
			stage := f.run(t, "stage", "prepared", "", false)
			for id, before := range oldEntries {
				if e := readMaintenanceSelection(f.runtimeRoot, before); e != nil {
					t.Fatal(e)
				}
				if pythonComponentInstalled(f.runtimeRoot, targets[id]) {
					t.Fatal("staging activated B")
				}
			}
			f.record["phase"], f.record["componentStage"] = "installing", stage
			f.save(t)
			env := f.environment(true)
			env.Python = newPython.Exe
			out, e := RunPythonComponentMaintenance(t.Context(), "activate", fixtureJSON(t, map[string]any{"transactionId": f.record["transactionId"]}), env)
			var ack struct{ Status string }
			if e != nil || json.Unmarshal(out, &ack) != nil || ack.Status != "activated" {
				t.Fatalf("target Python did not accept its own signed payloads: %s %v", out, e)
			}
			for id, target := range targets {
				if !slices.Contains(tc.ids, id) {
					if requests[target.URL] != 0 || pythonComponentInstalled(f.runtimeRoot, target) {
						t.Fatalf("installed unselected %s", id)
					}
					continue
				}
				if requests[target.URL] != 1 {
					t.Fatalf("%s download count %d", id, requests[target.URL])
				}
				if e := readMaintenanceSelection(f.runtimeRoot, target); e != nil {
					t.Fatal(e)
				}
				checkReplacementImport(t, newPython, filepath.Join(pythonComponentDir(f.runtimeRoot, target), "site-packages"), id, "B", true)
			}
		})
	}
}
