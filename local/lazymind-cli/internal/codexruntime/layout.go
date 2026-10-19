package codexruntime

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Resolve only an actual bundled CLI, never an unrelated PATH installation.
// Unknown Store/package layouts fail before startup configuration is changed.
func windowsDesktopLayout(binary string) (string, string, error) {
	cli, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return "", "", err
	}
	for dir := filepath.Dir(cli); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		resources := filepath.Join(dir, "resources")
		candidates := []string{filepath.Join(resources, "codex.exe"), filepath.Join(resources, "codex-cli", "codex.exe"), filepath.Join(resources, "codex-cli", "bin", "codex.exe")}
		match := false
		for _, p := range candidates {
			if strings.EqualFold(filepath.Clean(p), filepath.Clean(cli)) {
				match = true
			}
		}
		if !match {
			continue
		}
		if _, err := windowsDesktopExecutable(dir); err != nil {
			return "", "", err
		}
		nodes := []string{}
		for _, p := range []string{"node.exe", filepath.Join("bin", "node.exe")} {
			p = filepath.Join(resources, "cua_node", p)
			if info, e := os.Stat(p); e == nil && info.Mode().IsRegular() {
				nodes = append(nodes, p)
			}
		}
		if len(nodes) == 1 {
			return dir, nodes[0], nil
		}
		return "", "", errors.New("unsupported or ambiguous Windows Desktop layout")
	}
	return "", "", errors.New("shared mode requires the Windows Desktop bundled CLI")
}

// A Store package can contain both a launcher and the desktop executable. Only
// its own manifest may disambiguate them; unrelated manifests and entrypoints
// outside this application directory must not authorize a desktop executable.
func windowsDesktopExecutable(app string) (string, error) {
	app, err := filepath.EvalSymlinks(app)
	if err != nil {
		return "", err
	}
	var candidates []string
	for _, name := range []string{"Codex.exe", "ChatGPT.exe"} {
		file := filepath.Join(app, name)
		if info, err := os.Lstat(file); err == nil && info.Mode().IsRegular() {
			candidates = append(candidates, file)
		}
	}
	for _, root := range []string{app, filepath.Dir(app)} {
		file := filepath.Join(root, "AppxManifest.xml")
		info, err := os.Lstat(file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("MSIX manifest must be a regular file")
		}
		manifest, err := os.Open(file)
		if err != nil {
			return "", err
		}
		var pkg struct {
			XMLName  xml.Name `xml:"Package"`
			Identity struct {
				Name string `xml:"Name,attr"`
			} `xml:"Identity"`
			Applications []struct {
				Executable string `xml:"Executable,attr"`
			} `xml:"Applications>Application"`
		}
		err = xml.NewDecoder(io.LimitReader(manifest, 1<<20)).Decode(&pkg)
		manifest.Close()
		if err != nil || pkg.Identity.Name != "OpenAI.Codex" {
			return "", errors.New("invalid Codex MSIX manifest")
		}
		selected := map[string]bool{}
		for _, entry := range pkg.Applications {
			relative := filepath.FromSlash(strings.ReplaceAll(entry.Executable, `\`, "/"))
			if strings.ContainsAny(relative, ":\x00") || !filepath.IsLocal(relative) {
				return "", errors.New("invalid MSIX executable path")
			}
			resolved := filepath.Join(root, relative)
			for _, candidate := range candidates {
				if strings.EqualFold(resolved, candidate) {
					selected[candidate] = true
				}
			}
		}
		if len(selected) != 1 {
			return "", errors.New("unsupported or ambiguous MSIX Desktop entrypoint")
		}
		for candidate := range selected {
			return candidate, nil
		}
	}
	if len(candidates) != 1 {
		return "", fmt.Errorf("expected one Windows Desktop executable without an MSIX manifest, found %d", len(candidates))
	}
	return candidates[0], nil
}
