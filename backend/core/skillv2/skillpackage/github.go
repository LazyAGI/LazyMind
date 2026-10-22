package skillpackage

import (
	"archive/zip"
	"fmt"
	"path"
	"strings"
)

// ReadGitHubZip selects one Skill from a repository archive. Repository home
// links are accepted only when the archive contains exactly one Skill directory.
// Discovery reads ZIP metadata only; package limits apply after selection.
func ReadGitHubZip(zipPath, prefix string) (Package, error) {
	if prefix != "" {
		return ReadZipSubdirectory(zipPath, prefix)
	}
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return Package{}, err
	}
	defer reader.Close()
	root, err := singleArchiveRoot(reader.File)
	if err != nil {
		return Package{}, err
	}
	selected := ""
	found := false
	for _, entry := range reader.File {
		if isIgnoredMetadata(entry.Name) || entry.FileInfo().IsDir() {
			continue
		}
		name, err := CleanPath(entry.Name)
		if err != nil {
			return Package{}, err
		}
		if !strings.EqualFold(path.Base(name), "SKILL.md") {
			continue
		}
		if root != "" {
			name = strings.TrimPrefix(name, root+"/")
		}
		directory := path.Dir(name)
		if directory == "." {
			directory = ""
		}
		if found && selected != directory {
			return Package{}, fmt.Errorf("GitHub repository contains multiple skills; use a specific skill directory or SKILL.md URL")
		}
		found, selected = true, directory
	}
	if !found {
		return Package{}, fmt.Errorf("skill package must contain SKILL.md")
	}
	if selected != "" {
		return ReadZipSubdirectory(zipPath, selected)
	}
	pkg, err := ReadZip(zipPath)
	if err == nil {
		err = NormalizeSkillDocument(pkg.Files)
	}
	return pkg, err
}
