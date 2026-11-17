package workflowmcp

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func readValueFile(path string) ([]byte, error) {
	f, err := openLocalInputFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxLocalArtifactBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxLocalArtifactBytes || !utf8.Valid(raw) {
		return nil, errors.New("value file must be UTF-8 within 20 MiB")
	}
	return raw, nil
}

func readArgumentFile(path string) (map[string]any, error) {
	raw, err := readValueFile(path)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, errors.New("arguments_file must contain one JSON object")
	}
	return value, nil
}

func validateResultDirectory(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("result_directory must be an absolute Connector-local directory")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("result_directory must exist and be a directory")
	}
	return nil
}

func saveJSONValue(directory string, value any) (map[string]any, error) {
	if err := validateResultDirectory(directory); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	return saveExecutionResult(directory, object)
}

func withContractFile(directory string, invoke func() (BeginResult, error)) (BeginResult, error) {
	if directory != "" {
		if err := validateResultDirectory(directory); err != nil {
			return BeginResult{}, err
		}
	}
	result, err := invoke()
	if err != nil || directory == "" || result.Execution.ExecutionHandle == "" {
		return result, err
	}
	contract := result.Execution.StepContract
	raw, err := json.Marshal(contract)
	if err != nil {
		return result, err
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return result, err
	}
	reference, err := saveExecutionResult(directory, value)
	if err != nil {
		return result, err
	}
	result.Execution.StepContractFile = reference
	result.Execution.StepContract = StepContract{
		ContractVersion: contract.ContractVersion, SessionID: contract.SessionID, AttemptID: contract.AttemptID,
		StepID: contract.StepID, AttemptNo: contract.AttemptNo, WorkflowRevision: contract.WorkflowRevision,
		DeclaredOutputs: contract.DeclaredOutputs, DeclaredOutputTypes: contract.DeclaredOutputTypes,
		RequiredOutputs: contract.RequiredOutputs, OutputCardinality: contract.OutputCardinality,
	}
	return result, nil
}

func saveExecutionResult(parent string, result map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if len(raw) > 32<<20 {
		return nil, errors.New("tool result exceeds 32 MiB")
	}
	dir, err := os.MkdirTemp(parent, "workflow-tool-")
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(dir)
		}
	}()
	responsePath := filepath.Join(dir, "response.json")
	if err := os.WriteFile(responsePath, raw, 0600); err != nil {
		return nil, err
	}
	summary := map[string]any{"result_path": responsePath, "size_bytes": len(raw), "sha256": fmt.Sprintf("%x", sha256.Sum256(raw))}
	files := []any{}
	// Tool adapters may be nested in the Core result envelope. Do not walk
	// arbitrary payload fields or return generated prose into the controller.
	value := result
	for depth := 0; depth < 4 && value != nil; depth++ {
		for _, key := range []string{"status", "page_count", "page_no", "validation_owner", "error"} {
			if v, exists := value[key]; exists {
				summary[key] = v
			}
		}
		if _, exists := value["generation_request"]; exists {
			summary["status"] = "generation_required"
		}
		if list, exists := value["files"].([]any); exists {
			for _, item := range list {
				file, valid := item.(map[string]any)
				if !valid {
					return nil, errors.New("invalid tool file record")
				}
				name, _ := file["name"].(string)
				encoded, _ := file["content_base64"].(string)
				if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, "\\\x00") || name == "response.json" {
					return nil, errors.New("unsafe tool output filename")
				}
				data, err := base64.StdEncoding.Strict().DecodeString(encoded)
				if err != nil || len(data) > 24<<20 {
					return nil, errors.New("invalid or oversized tool file")
				}
				hash := fmt.Sprintf("%x", sha256.Sum256(data))
				if expected, exists := file["sha256"].(string); exists && strings.TrimPrefix(expected, "sha256:") != hash {
					return nil, errors.New("tool file hash mismatch")
				}
				path := filepath.Join(dir, name)
				f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					return nil, err
				}
				_, writeErr := f.Write(data)
				closeErr := f.Close()
				if writeErr != nil {
					return nil, writeErr
				}
				if closeErr != nil {
					return nil, closeErr
				}
				files = append(files, map[string]any{"local_path": path, "name": name, "sha256": hash, "size_bytes": len(data), "mime_type": file["mime_type"]})
			}
		}
		value, _ = value["result"].(map[string]any)
	}
	summary["files"] = files
	ok = true
	return summary, nil
}
