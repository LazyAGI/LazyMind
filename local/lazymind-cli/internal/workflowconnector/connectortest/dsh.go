package connectortest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/workflowconnector"
)

func WriteDSHPackage(t testing.TB, executable, build string) workflowconnector.Manifest {
	t.Helper()
	version, _, _ := strings.Cut(build, "+")
	contents := map[string]string{
		"package.json":     fmt.Sprintf(`{"name":"@lazymind/dsh-workflow","version":%q,"type":"module","exports":{".":"./lib/index.js","./client":"./lib/client.js"},"dsh":{"bundle":{"patch":"./cordis.patch.yml"},"client":{"platform":"web"}}}`, version),
		"lib/index.js":     fmt.Sprintf("export const connectorBuild = %q\n", build),
		"lib/client.js":    fmt.Sprintf("window.__ModuleLoader__.load({ id: '@lazymind/dsh-workflow', factory: () => ({ connectorBuild: %q }) })\n", build),
		"cordis.patch.yml": "[]",
	}
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	writer := tar.NewWriter(gzipWriter)
	names := make([]string, 0, len(contents))
	for name := range contents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		content := contents[name]
		if err := writer.WriteHeader(&tar.Header{Name: "package/" + name, Mode: 0644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(writer, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	const entry = "dsh-workflow.tgz"
	sum := sha256.Sum256(archive.Bytes())
	hash := sha256.New()
	hash.Write([]byte(entry + "\x00"))
	hash.Write(sum[:])
	manifest := workflowconnector.Manifest{Schema: 1, Host: workflowconnector.DSHHost, Version: build,
		Release: hex.EncodeToString(hash.Sum(nil)), Entry: entry, Files: []string{entry}}
	dir := filepath.Join(filepath.Dir(executable), "workflow-connectors", workflowconnector.DSHHost)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, entry), archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "connector.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func UnpackDSHFixture(t testing.TB, archive, directory string) {
	t.Helper()
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	unzip, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer unzip.Close()
	reader := tar.NewReader(unzip)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		name, ok := strings.CutPrefix(header.Name, "package/")
		if !ok || strings.Contains(name, "..") {
			t.Fatalf("unexpected fixture archive member %q", header.Name)
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
