package skillpatch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type AppliedPatch struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}

type Result struct {
	Files          map[string][]byte
	AppliedPatches []AppliedPatch
	PatchSetSHA256 string
	SkillMDPatched bool
}

func Apply(target Target, files map[string][]byte, catalog Catalog) (Result, error) {
	result := Result{Files: cloneFiles(files)}
	for _, patch := range catalog.patches {
		if patch.Target.UID != target.UID || patch.Target.Version != target.Version {
			continue
		}
		if patch.Target.OriginTreeHash != target.OriginTreeHash {
			return Result{}, patchFailure("Skill patch %s origin tree mismatch: got %s, want %s", patch.ID, target.OriginTreeHash, patch.Target.OriginTreeHash)
		}
		for _, operation := range patch.Operations {
			if err := applyOperation(result.Files, patch.ID, operation); err != nil {
				return Result{}, err
			}
			if operation.Path == "SKILL.md" {
				result.SkillMDPatched = true
			}
		}
		result.AppliedPatches = append(result.AppliedPatches, AppliedPatch{ID: patch.ID, SHA256: patch.SHA256})
	}
	if len(result.AppliedPatches) > 0 {
		body, err := json.Marshal(result.AppliedPatches)
		if err != nil {
			return Result{}, err
		}
		hash := sha256.Sum256(body)
		result.PatchSetSHA256 = hex.EncodeToString(hash[:])
	}
	return result, nil
}

func (catalog Catalog) ValidateApplied(counts map[string]int) error {
	for _, patch := range catalog.patches {
		if counts[patch.ID] != 1 {
			return patchFailure("Skill patch %s applied %d times, want exactly once", patch.ID, counts[patch.ID])
		}
	}
	return nil
}

func applyOperation(files map[string][]byte, patchID string, operation Operation) error {
	current, exists := files[operation.Path]
	if operation.BeforeSHA256 == "absent" {
		if exists {
			return patchFailure("Skill patch %s expected %s to be absent", patchID, operation.Path)
		}
	} else {
		if !exists {
			return patchFailure("Skill patch %s expected %s to exist", patchID, operation.Path)
		}
		hash := sha256.Sum256(current)
		actual := hex.EncodeToString(hash[:])
		if actual != operation.BeforeSHA256 {
			return patchFailure("Skill patch %s file %s hash mismatch: got %s, want %s", patchID, operation.Path, actual, operation.BeforeSHA256)
		}
	}

	switch operation.Op {
	case "upsert":
		files[operation.Path] = append([]byte(nil), operation.Content...)
	case "delete":
		delete(files, operation.Path)
	default:
		return patchFailure("Skill patch %s has unsupported operation %q", patchID, operation.Op)
	}
	return nil
}

func cloneFiles(files map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(files))
	for path, content := range files {
		out[path] = append([]byte(nil), content...)
	}
	return out
}
