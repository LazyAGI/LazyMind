package systemdeps

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMaintenanceProbeVerifiesEnabledBundledSharedWheels(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil && runtime.GOOS == "windows" {
		python, err = exec.LookPath("python")
	}
	if err != nil {
		t.Skip("Python is unavailable")
	}
	root := t.TempDir()
	// Prepare an actual venv and PEP 376 metadata. The shared distribution is
	// visible through a relative .pth, as in the bundled macOS environment.
	const fixture = `import hashlib, json, os, pathlib, platform, subprocess, sys, venv
root = pathlib.Path(sys.argv[1]).resolve()
prefix = root / 'deps' / 'python' / 'algorithm'
venv.EnvBuilder(with_pip=False).create(prefix)
python = prefix / ('Scripts/python.exe' if os.name == 'nt' else 'bin/python')
base = pathlib.Path(subprocess.check_output([str(python), '-I', '-c', 'import sysconfig; print(sysconfig.get_path("purelib"))'], text=True).strip())
digest = '7' * 64
shared = prefix.parent / 'shared' / digest
overlay = root / 'overlay'
pth = base / ('lazymind-shared-' + digest + '.pth')
identity = {'schemaVersion': 1, 'platform': 'windows' if sys.platform == 'win32' else sys.platform,
            'arch': 'amd64' if platform.machine().lower() in {'amd64', 'x86_64'} else 'arm64',
            'pythonAbi': f'cp{sys.version_info.major}{sys.version_info.minor}'}
versions, records = {}, {}
def wheel(name, site):
    info = site / (name.replace('-', '_') + '-1.dist-info')
    info.mkdir(parents=True)
    (info / 'METADATA').write_text(f'Name: {name}\nVersion: 1\n', encoding='utf-8')
    record = name + '\n'
    (info / 'RECORD').write_text(record, encoding='utf-8')
    versions[name] = '1'
    records[name] = hashlib.sha256(record.encode()).hexdigest()
def fingerprint():
    encoded = json.dumps({'identity': identity, 'versions': versions, 'records': records}, sort_keys=True, separators=(',', ':')) + '\n'
    return hashlib.sha256(encoded.encode()).hexdigest()
wheel('base-dep', base)
wheel('shared-dep', shared)
base_fingerprint = fingerprint()
wheel('umap-learn', overlay)
for module in ('llvmlite', 'numba', 'pynndescent', 'umap'):
    (overlay / (module + '.py')).write_text('VALUE = 1\n', encoding='utf-8')
pth.write_text(os.path.relpath(shared, base) + '\n', encoding='utf-8')
print(json.dumps({'python': str(python), 'pth': str(pth), 'shared': str(shared), 'overlay': str(overlay),
    'base': base_fingerprint, 'full': fingerprint(), 'abi': identity['pythonAbi']}))
`
	output, err := exec.CommandContext(t.Context(), python, "-I", "-B", "-c", fixture, root).CombinedOutput()
	if err != nil {
		t.Fatalf("prepare shared Python fixture: %v\n%s", err, output)
	}
	var f struct{ Python, Pth, Shared, Overlay, Base, Full, ABI string }
	if err := json.Unmarshal(output, &f); err != nil {
		t.Fatalf("read shared Python fixture: %v\n%s", err, output)
	}
	if err := os.MkdirAll(filepath.Join(root, "deps", "python-components"), 0700); err != nil {
		t.Fatal(err)
	}
	environment := PythonComponentMaintenanceContext{RuntimeRoot: root, Python: f.Python, AlgorithmSource: root}
	selected := map[string]componentProbeInput{"cluster": {Path: f.Overlay, Descriptor: PythonComponent{
		Platform: runtime.GOOS, Arch: runtime.GOARCH, PythonABI: f.ABI, BaseFingerprint: f.Full,
		Packages: map[string]string{"umap-learn": "1"},
	}}}
	for _, fingerprint := range []string{f.Base, ""} {
		if err := verifyTargetComponents(t.Context(), selected, fingerprint, environment); err != nil {
			t.Fatalf("enabled bundled shared wheel rejected with base fingerprint %q: %v", fingerprint, err)
		}
	}
	if err := os.Remove(f.Pth); err != nil {
		t.Fatal(err)
	}
	if err := verifyTargetComponents(t.Context(), selected, f.Base, environment); err == nil {
		t.Fatal("disabled bundled shared wheel authorized the update")
	}
	foreign := filepath.Join(root, "foreign", filepath.Base(f.Shared))
	if err := os.Mkdir(filepath.Dir(foreign), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.Shared, foreign); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.Pth, []byte(foreign+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyTargetComponents(t.Context(), selected, f.Base, environment); err == nil {
		t.Fatal("wheel outside the bundled shared directory authorized the update")
	}
}
