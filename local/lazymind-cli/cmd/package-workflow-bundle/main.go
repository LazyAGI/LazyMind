// package-workflow-bundle creates the deterministic precompiled plugin embedded in the connector.
package main

import (
	"archive/tar"
	"compress/gzip"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func main() {
	source := flag.String("source", "../../integrations/dsh-workflow", "built bundle directory")
	output := flag.String("output", "internal/adapters/mcpclient/assets/dsh-workflow.tgz", "output archive")
	flag.Parse()
	if err := pack(*source, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func pack(source, output string) error {
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(output), ".bundle-*.tgz")
	if err != nil {
		return err
	}
	defer file.Close()
	defer os.Remove(file.Name())
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, name := range []string{"package.json", "lib/index.js", "lib/client.js", "cordis.patch.yml", "README.md"} {
		body, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		header := &tar.Header{Name: "package/" + name, Mode: 0644, Size: int64(len(body)), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if _, err := tarWriter.Write(body); err != nil {
			return err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return err
	}
	if err := gzipWriter.Close(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), output)
}
