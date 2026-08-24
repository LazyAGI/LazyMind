package builtin

import (
	"path"
	"strings"

	skillpackage "lazymind/core/skillv2/skillpackage"
)

const TemplateIDPrefix = "builtin:"

type Package struct {
	UID           string
	Category      string
	Name          string
	Description   string
	Version       string
	SHA256        string
	SourceURL     string
	ArchivePath   string
	Tags          []string
	MarketVisible bool
	Files         map[string][]byte
}

func TemplateID(uid string) string {
	return TemplateIDPrefix + strings.TrimSpace(uid)
}

func IsTemplateID(id string) bool {
	return strings.HasPrefix(id, TemplateIDPrefix)
}

func SkillContent(templateID string) (content, name string, ok bool, err error) {
	if !IsTemplateID(templateID) {
		return "", "", false, nil
	}
	uid := strings.TrimPrefix(templateID, TemplateIDPrefix)
	parentUID := uid
	relativePath := ""
	if idx := strings.IndexByte(uid, ':'); idx >= 0 {
		parentUID = uid[:idx]
		relativePath = uid[idx+1:]
	}
	if relativePath != "" {
		if _, err := skillpackage.CleanPath(relativePath); err != nil {
			return "", "", false, nil
		}
	}
	pkg, found, err := PackageByUID(parentUID)
	if err != nil || !found {
		return "", "", false, err
	}
	if relativePath == "" {
		data, ok := pkg.Files["SKILL.md"]
		return string(data), pkg.Name, ok, nil
	}
	data, ok := pkg.Files[relativePath]
	if !ok {
		return "", "", false, nil
	}
	name = strings.TrimSuffix(relativePath, path.Ext(relativePath))
	return string(data), name, true, nil
}

func PackageByUID(uid string) (Package, bool, error) {
	return catalogPackageByUID(CatalogPath(), strings.TrimSpace(uid))
}

func Packages() ([]Package, error) {
	return catalogPackages(CatalogPath())
}
