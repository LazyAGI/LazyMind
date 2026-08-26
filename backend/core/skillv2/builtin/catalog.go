package builtin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	skillpackage "lazymind/core/skillv2/skillpackage"
)

const (
	CatalogSchemaVersion = 1
	maxProviderNameRunes = 64
)

type Catalog struct {
	SchemaVersion int            `json:"schema_version"`
	Skills        []CatalogSkill `json:"skills"`
}

type catalogError string

func (err catalogError) Error() string { return string(err) }

func catalogFailure(format string, args ...any) error {
	return catalogError(fmt.Sprintf(format, args...))
}

type CatalogSkill struct {
	Key                 string         `json:"key"`
	UID                 string         `json:"uid"`
	SourceURL           string         `json:"source_url"`
	ResolvedURL         string         `json:"resolved_url"`
	Version             string         `json:"version"`
	Name                string         `json:"name"`
	Description         string         `json:"description"`
	Category            string         `json:"category"`
	Tags                []string       `json:"tags,omitempty"`
	Provider            string         `json:"provider,omitempty"`
	MarketVisible       *bool          `json:"market_visible,omitempty"`
	Content             string         `json:"content,omitempty"`
	OriginArchiveSHA256 string         `json:"origin_archive_sha256,omitempty"`
	OriginTreeSHA256    string         `json:"origin_tree_sha256,omitempty"`
	OriginArchiveSize   int64          `json:"origin_archive_size,omitempty"`
	PatchSetSHA256      string         `json:"patch_set_sha256,omitempty"`
	AppliedPatches      []CatalogPatch `json:"applied_patches,omitempty"`
	ArchiveSHA256       string         `json:"archive_sha256"`
	TreeSHA256          string         `json:"tree_sha256"`
	ArchiveSize         int64          `json:"archive_size"`
	PackageFile         string         `json:"package_file"`
}

type CatalogPatch struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}

func CatalogPath() string {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return ""
	}
	return firstCatalogPath(catalogPathCandidates(workingDirectory, "builtin-skills"))
}

func catalogPathCandidates(workingDirectory, catalogDirectory string) []string {
	return []string{
		filepath.Join(string(filepath.Separator), "skills", ".runtime", catalogDirectory, "catalog.json"),
		filepath.Join(workingDirectory, "..", "..", "skills", ".runtime", catalogDirectory, "catalog.json"),
		filepath.Join(workingDirectory, "..", "..", "..", catalogDirectory, "catalog.json"),
	}
}

func firstCatalogPath(candidates []string) string {
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			absolute, absErr := filepath.Abs(candidate)
			if absErr == nil {
				return filepath.Clean(absolute)
			}
		}
	}
	return ""
}

func LoadCatalog(catalogPath string) (Catalog, error) {
	body, err := os.ReadFile(catalogPath)
	if err != nil {
		return Catalog{}, err
	}
	var catalog Catalog
	if err := json.Unmarshal(body, &catalog); err != nil {
		return Catalog{}, catalogFailure("parse builtin skill catalog %s: %v", catalogPath, err)
	}
	if catalog.SchemaVersion != CatalogSchemaVersion {
		return Catalog{}, catalogFailure("unsupported builtin skill catalog schema %d", catalog.SchemaVersion)
	}
	seen := make(map[string]struct{}, len(catalog.Skills))
	for i := range catalog.Skills {
		entry := &catalog.Skills[i]
		entry.UID = strings.TrimSpace(entry.UID)
		entry.Key = strings.TrimSpace(entry.Key)
		entry.SourceURL = strings.TrimSpace(entry.SourceURL)
		entry.ResolvedURL = strings.TrimSpace(entry.ResolvedURL)
		entry.Version = strings.TrimSpace(entry.Version)
		entry.Name = strings.TrimSpace(entry.Name)
		entry.Description = strings.TrimSpace(entry.Description)
		entry.Category = strings.TrimSpace(entry.Category)
		provider, err := NormalizeProviderName(entry.Provider)
		if err != nil {
			return Catalog{}, catalogFailure("builtin skill %s: %v", entry.UID, err)
		}
		entry.Provider = provider
		entry.PackageFile = strings.TrimSpace(entry.PackageFile)
		entry.ArchiveSHA256 = strings.ToLower(strings.TrimSpace(entry.ArchiveSHA256))
		entry.TreeSHA256 = strings.ToLower(strings.TrimSpace(entry.TreeSHA256))
		entry.OriginArchiveSHA256 = strings.ToLower(strings.TrimSpace(entry.OriginArchiveSHA256))
		entry.OriginTreeSHA256 = strings.ToLower(strings.TrimSpace(entry.OriginTreeSHA256))
		entry.PatchSetSHA256 = strings.ToLower(strings.TrimSpace(entry.PatchSetSHA256))
		if entry.Key == "" || entry.UID == "" || entry.SourceURL == "" || entry.ResolvedURL == "" || entry.Version == "" || entry.Name == "" || entry.Description == "" || entry.Category == "" || entry.PackageFile == "" {
			return Catalog{}, catalogFailure("builtin skill catalog entry %d is incomplete", i)
		}
		if _, exists := seen[entry.UID]; exists {
			return Catalog{}, catalogFailure("duplicate builtin skill uid %q", entry.UID)
		}
		seen[entry.UID] = struct{}{}
		if len(entry.ArchiveSHA256) != sha256.Size*2 {
			return Catalog{}, catalogFailure("builtin skill %s has invalid archive sha256", entry.UID)
		}
		if _, err := hex.DecodeString(entry.ArchiveSHA256); err != nil {
			return Catalog{}, catalogFailure("builtin skill %s has invalid archive sha256: %v", entry.UID, err)
		}
		if len(entry.TreeSHA256) != sha256.Size*2 {
			return Catalog{}, catalogFailure("builtin skill %s has invalid tree sha256", entry.UID)
		}
		if _, err := hex.DecodeString(entry.TreeSHA256); err != nil {
			return Catalog{}, catalogFailure("builtin skill %s has invalid tree sha256: %v", entry.UID, err)
		}
		if entry.ArchiveSize <= 0 {
			return Catalog{}, catalogFailure("builtin skill %s has invalid archive size", entry.UID)
		}
		if err := validatePatchProvenance(entry); err != nil {
			return Catalog{}, catalogFailure("builtin skill %s: %v", entry.UID, err)
		}
		if _, err := resolvePackagePath(catalogPath, entry.PackageFile); err != nil {
			return Catalog{}, catalogFailure("builtin skill %s: %v", entry.UID, err)
		}
	}
	return catalog, nil
}

func validatePatchProvenance(entry *CatalogSkill) error {
	hasProvenance := entry.OriginArchiveSHA256 != "" || entry.OriginTreeSHA256 != "" || entry.OriginArchiveSize != 0 || entry.PatchSetSHA256 != "" || len(entry.AppliedPatches) > 0
	if !hasProvenance {
		return nil
	}
	if len(entry.AppliedPatches) == 0 || entry.OriginArchiveSize <= 0 {
		return catalogFailure("patch provenance is incomplete")
	}
	for name, value := range map[string]string{
		"origin archive sha256": entry.OriginArchiveSHA256,
		"origin tree sha256":    entry.OriginTreeSHA256,
		"patch set sha256":      entry.PatchSetSHA256,
	} {
		if len(value) != sha256.Size*2 {
			return catalogFailure("%s is invalid", name)
		}
		if _, err := hex.DecodeString(value); err != nil {
			return catalogFailure("%s is invalid: %v", name, err)
		}
	}
	seen := make(map[string]bool, len(entry.AppliedPatches))
	for index := range entry.AppliedPatches {
		patch := &entry.AppliedPatches[index]
		patch.ID = strings.TrimSpace(patch.ID)
		patch.SHA256 = strings.ToLower(strings.TrimSpace(patch.SHA256))
		if patch.ID == "" || len(patch.SHA256) != sha256.Size*2 {
			return catalogFailure("applied patch %d is invalid", index)
		}
		if _, err := hex.DecodeString(patch.SHA256); err != nil {
			return catalogFailure("applied patch %s has invalid sha256: %v", patch.ID, err)
		}
		if seen[patch.ID] {
			return catalogFailure("duplicate applied patch %s", patch.ID)
		}
		seen[patch.ID] = true
	}
	return nil
}

func catalogPackages(catalogPath string) ([]Package, error) {
	if catalogPath == "" {
		return nil, nil
	}
	catalog, err := LoadCatalog(catalogPath)
	if err != nil {
		return nil, err
	}
	packages := make([]Package, 0, len(catalog.Skills))
	for _, entry := range catalog.Skills {
		if strings.TrimSpace(entry.Content) == "" {
			return nil, catalogFailure("builtin skill %s is missing catalog content", entry.UID)
		}
		archivePath, _ := resolvePackagePath(catalogPath, entry.PackageFile)
		packages = append(packages, packageFromCatalog(entry, archivePath, map[string][]byte{"SKILL.md": []byte(entry.Content)}))
	}
	return packages, nil
}

func catalogPackageByUID(catalogPath, uid string) (Package, bool, error) {
	if catalogPath == "" {
		return Package{}, false, nil
	}
	catalog, err := LoadCatalog(catalogPath)
	if err != nil {
		return Package{}, false, err
	}
	for _, entry := range catalog.Skills {
		if entry.UID != uid {
			continue
		}
		archivePath, err := resolvePackagePath(catalogPath, entry.PackageFile)
		if err != nil {
			return Package{}, false, err
		}
		if err := verifyArchive(archivePath, entry.ArchiveSHA256, entry.ArchiveSize); err != nil {
			return Package{}, false, catalogFailure("builtin skill %s: %v", uid, err)
		}
		pkg, err := skillpackage.ReadZip(archivePath)
		if err != nil {
			return Package{}, false, err
		}
		files := pkg.Files
		if _, ok := files["SKILL.md"]; !ok {
			return Package{}, false, catalogFailure("builtin skill %s missing SKILL.md", uid)
		}
		return packageFromCatalog(entry, archivePath, files), true, nil
	}
	return Package{}, false, nil
}

func packageFromCatalog(entry CatalogSkill, archivePath string, files map[string][]byte) Package {
	return Package{
		UID:           entry.UID,
		Category:      entry.Category,
		Name:          entry.Name,
		Description:   entry.Description,
		Version:       entry.Version,
		SHA256:        entry.ArchiveSHA256,
		TreeSHA256:    entry.TreeSHA256,
		SourceURL:     entry.SourceURL,
		Provider:      entry.Provider,
		ArchivePath:   archivePath,
		Tags:          append([]string(nil), entry.Tags...),
		MarketVisible: CatalogSkillMarketVisible(entry),
		Files:         files,
	}
}

// NormalizeProviderName validates the optional attribution shown by the Skill Plaza.
func NormalizeProviderName(raw string) (string, error) {
	provider := strings.TrimSpace(raw)
	if strings.ContainsAny(provider, "\r\n\t") {
		return "", catalogFailure("provider must be a single-line label")
	}
	if utf8.RuneCountInString(provider) > maxProviderNameRunes {
		return "", catalogFailure("provider exceeds %d characters", maxProviderNameRunes)
	}
	return provider, nil
}

func CatalogSkillMarketVisible(entry CatalogSkill) bool {
	return entry.MarketVisible == nil || *entry.MarketVisible
}

func resolvePackagePath(catalogPath, packageFile string) (string, error) {
	cleaned, err := skillpackage.CleanPath(filepath.ToSlash(packageFile))
	if err != nil {
		return "", catalogFailure("invalid package_file: %v", err)
	}
	base := filepath.Dir(catalogPath)
	candidate := filepath.Join(base, filepath.FromSlash(cleaned))
	rel, err := filepath.Rel(base, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", catalogFailure("package_file escapes catalog directory")
	}
	return candidate, nil
}

func verifyArchive(archivePath, expectedHash string, expectedSize int64) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if expectedSize > 0 && info.Size() != expectedSize {
		return catalogFailure("archive size mismatch: got %d, want %d", info.Size(), expectedSize)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expectedHash {
		return catalogFailure("archive sha256 mismatch: got %s, want %s", actual, expectedHash)
	}
	return nil
}
