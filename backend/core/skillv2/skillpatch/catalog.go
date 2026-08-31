package skillpatch

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	skillpackage "lazymind/core/skillv2/skillpackage"
)

const SchemaVersion = 1

type patchError string

func (err patchError) Error() string { return string(err) }

func patchFailure(format string, args ...any) error {
	return patchError(fmt.Sprintf(format, args...))
}

type Catalog struct {
	patches []Patch
}

type Patch struct {
	ID          string
	Description string
	Target      Target
	Operations  []Operation
	SHA256      string
}

type Target struct {
	UID            string `json:"uid" yaml:"uid"`
	Version        string `json:"version" yaml:"version"`
	OriginTreeHash string `json:"origin_tree_sha256" yaml:"origin_tree_sha256"`
}

type Operation struct {
	Op           string
	Path         string
	File         string
	BeforeSHA256 string
	Content      []byte
}

type rawCatalog struct {
	SchemaVersion int      `yaml:"schema_version"`
	Patches       []string `yaml:"patches"`
}

type rawPatch struct {
	SchemaVersion int            `yaml:"schema_version"`
	ID            string         `yaml:"id"`
	Description   string         `yaml:"description,omitempty"`
	Target        Target         `yaml:"target"`
	Operations    []rawOperation `yaml:"operations"`
}

type rawOperation struct {
	Op           string `yaml:"op"`
	Path         string `yaml:"path"`
	File         string `yaml:"file,omitempty"`
	BeforeSHA256 string `yaml:"before_sha256"`
}

type digestPatch struct {
	SchemaVersion int               `json:"schema_version"`
	ID            string            `json:"id"`
	Description   string            `json:"description,omitempty"`
	Target        Target            `json:"target"`
	Operations    []digestOperation `json:"operations"`
}

type digestOperation struct {
	Op            string `json:"op"`
	Path          string `json:"path"`
	File          string `json:"file,omitempty"`
	BeforeSHA256  string `json:"before_sha256"`
	ContentSHA256 string `json:"content_sha256,omitempty"`
}

func LoadCatalog(catalogPath string) (Catalog, error) {
	var raw rawCatalog
	if err := decodeYAMLFile(catalogPath, &raw); err != nil {
		return Catalog{}, err
	}
	if raw.SchemaVersion != SchemaVersion {
		return Catalog{}, patchFailure("unsupported Skill patch catalog schema %d", raw.SchemaVersion)
	}

	catalogRoot := filepath.Dir(catalogPath)
	seenPaths := make(map[string]bool, len(raw.Patches))
	seenIDs := make(map[string]bool, len(raw.Patches))
	catalog := Catalog{patches: make([]Patch, 0, len(raw.Patches))}
	for _, value := range raw.Patches {
		patchPath, err := skillpackage.CleanPath(filepath.ToSlash(strings.TrimSpace(value)))
		if err != nil {
			return Catalog{}, patchFailure("invalid Skill patch path %q: %v", value, err)
		}
		if filepath.Base(filepath.FromSlash(patchPath)) != "patch.yaml" {
			return Catalog{}, patchFailure("Skill patch path must end in patch.yaml: %s", patchPath)
		}
		if seenPaths[patchPath] {
			return Catalog{}, patchFailure("duplicate Skill patch path %s", patchPath)
		}
		seenPaths[patchPath] = true

		body, resolvedPath, err := readContainedFile(catalogRoot, patchPath)
		if err != nil {
			return Catalog{}, patchFailure("load Skill patch %s: %v", patchPath, err)
		}
		var definition rawPatch
		if err := decodeYAML(resolvedPath, body, &definition); err != nil {
			return Catalog{}, err
		}
		patch, err := loadPatch(filepath.Dir(resolvedPath), definition)
		if err != nil {
			return Catalog{}, patchFailure("load Skill patch %s: %v", patchPath, err)
		}
		if seenIDs[patch.ID] {
			return Catalog{}, patchFailure("duplicate Skill patch id %s", patch.ID)
		}
		seenIDs[patch.ID] = true
		catalog.patches = append(catalog.patches, patch)
	}
	return catalog, nil
}

func loadPatch(patchRoot string, raw rawPatch) (Patch, error) {
	if raw.SchemaVersion != SchemaVersion {
		return Patch{}, patchFailure("unsupported patch schema %d", raw.SchemaVersion)
	}
	patch := Patch{
		ID:          strings.TrimSpace(raw.ID),
		Description: strings.TrimSpace(raw.Description),
		Target: Target{
			UID:            strings.TrimSpace(raw.Target.UID),
			Version:        strings.TrimSpace(raw.Target.Version),
			OriginTreeHash: strings.ToLower(strings.TrimSpace(raw.Target.OriginTreeHash)),
		},
	}
	if patch.ID == "" || patch.Target.UID == "" || patch.Target.Version == "" {
		return Patch{}, patchFailure("id and target uid/version are required")
	}
	if err := validateSHA256(patch.Target.OriginTreeHash); err != nil {
		return Patch{}, patchFailure("invalid target origin_tree_sha256: %v", err)
	}
	if len(raw.Operations) == 0 {
		return Patch{}, patchFailure("operations are required")
	}

	seenPaths := make(map[string]bool, len(raw.Operations))
	digest := digestPatch{
		SchemaVersion: SchemaVersion,
		ID:            patch.ID,
		Description:   patch.Description,
		Target:        patch.Target,
		Operations:    make([]digestOperation, 0, len(raw.Operations)),
	}
	for _, value := range raw.Operations {
		operation, digestOperation, err := loadOperation(patchRoot, value)
		if err != nil {
			return Patch{}, err
		}
		if seenPaths[operation.Path] {
			return Patch{}, patchFailure("duplicate operation path %s", operation.Path)
		}
		seenPaths[operation.Path] = true
		patch.Operations = append(patch.Operations, operation)
		digest.Operations = append(digest.Operations, digestOperation)
	}
	body, err := json.Marshal(digest)
	if err != nil {
		return Patch{}, err
	}
	hash := sha256.Sum256(body)
	patch.SHA256 = hex.EncodeToString(hash[:])
	return patch, nil
}

func loadOperation(patchRoot string, raw rawOperation) (Operation, digestOperation, error) {
	operation := Operation{
		Op:           strings.ToLower(strings.TrimSpace(raw.Op)),
		BeforeSHA256: strings.ToLower(strings.TrimSpace(raw.BeforeSHA256)),
	}
	cleanedPath, err := skillpackage.CleanPath(filepath.ToSlash(strings.TrimSpace(raw.Path)))
	if err != nil {
		return Operation{}, digestOperation{}, patchFailure("invalid operation path %q: %v", raw.Path, err)
	}
	operation.Path = cleanedPath

	switch operation.Op {
	case "upsert":
		filePath, err := skillpackage.CleanPath(filepath.ToSlash(strings.TrimSpace(raw.File)))
		if err != nil || !strings.HasPrefix(filePath, "files/") {
			return Operation{}, digestOperation{}, patchFailure("upsert %s requires a file under files/", operation.Path)
		}
		body, _, err := readContainedFile(patchRoot, filePath)
		if err != nil {
			return Operation{}, digestOperation{}, patchFailure("read payload %s: %v", filePath, err)
		}
		if len(body) > skillpackage.MaxFileBytes {
			return Operation{}, digestOperation{}, patchFailure("payload %s exceeds %d bytes", filePath, skillpackage.MaxFileBytes)
		}
		operation.File = filePath
		operation.Content = body
	case "delete":
		if strings.TrimSpace(raw.File) != "" {
			return Operation{}, digestOperation{}, patchFailure("delete %s cannot declare file", operation.Path)
		}
	default:
		return Operation{}, digestOperation{}, patchFailure("unsupported operation %q", operation.Op)
	}

	if operation.BeforeSHA256 != "absent" {
		if err := validateSHA256(operation.BeforeSHA256); err != nil {
			return Operation{}, digestOperation{}, patchFailure("invalid before_sha256 for %s: %v", operation.Path, err)
		}
	} else if operation.Op != "upsert" {
		return Operation{}, digestOperation{}, patchFailure("delete %s cannot expect absent content", operation.Path)
	}

	contentHash := ""
	if operation.Op == "upsert" {
		hash := sha256.Sum256(operation.Content)
		contentHash = hex.EncodeToString(hash[:])
	}
	return operation, digestOperation{
		Op:            operation.Op,
		Path:          operation.Path,
		File:          operation.File,
		BeforeSHA256:  operation.BeforeSHA256,
		ContentSHA256: contentHash,
	}, nil
}

func decodeYAMLFile(path string, value any) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return decodeYAML(path, body, value)
}

func decodeYAML(path string, body []byte, value any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	if err := decoder.Decode(value); err != nil {
		return patchFailure("parse %s: %v", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return patchFailure("parse %s: multiple YAML documents are not supported", path)
	}
	return nil
}

func readContainedFile(root, relativePath string) ([]byte, string, error) {
	rootPath, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, "", err
	}
	rootPath, err = filepath.Abs(rootPath)
	if err != nil {
		return nil, "", err
	}
	candidate := filepath.Join(rootPath, filepath.FromSlash(relativePath))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return nil, "", err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return nil, "", err
	}
	rel, err := filepath.Rel(rootPath, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, "", patchFailure("path escapes patch root")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, "", err
	}
	if info.IsDir() {
		return nil, "", patchFailure("path is a directory")
	}
	body, err := os.ReadFile(resolved)
	return body, resolved, err
}

func validateSHA256(value string) error {
	if len(value) != sha256.Size*2 {
		return patchFailure("expected %d hexadecimal characters", sha256.Size*2)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return err
	}
	return nil
}
