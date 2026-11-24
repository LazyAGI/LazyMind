package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"os"
	"reflect"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

const maxUpdateComponentCatalog = 1024 * 1024

func readV1ComponentCatalog(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, updateStateFailure("component_update_incompatible")
	}
	if info.Size() <= 0 || info.Size() > maxUpdateComponentCatalog {
		return nil, updateStateFailure("artifact_size")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, updateStateFailure("component_update_incompatible")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, updateStateFailure("component_update_incompatible")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxUpdateComponentCatalog+1))
	if err != nil {
		return nil, updateStateFailure("component_update_incompatible")
	}
	if int64(len(data)) != info.Size() {
		return nil, updateStateFailure("artifact_size")
	}
	return data, nil
}

func verifyV1CatalogBytes(data []byte, artifact desktopupdate.Artifact) error {
	if int64(len(data)) != artifact.Size {
		return updateStateFailure("artifact_size")
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != artifact.SHA256 {
		return updateStateFailure("artifact_hash")
	}
	return nil
}

type updateComponentResult struct {
	Status            string `json:"status"`
	Reason            string `json:"reason,omitempty"`
	DataProfile       string `json:"dataProfile,omitempty"`
	ReleaseID         string `json:"releaseId,omitempty"`
	AttemptID         string `json:"attemptId,omitempty"`
	PreviousAttemptID string `json:"previousAttemptId,omitempty"`
}

func checkUpdateComponents(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths, file, expectedHash string, expectedSize int64, request v1HandoffRequest) (updateComponentResult, error) {
	var result updateComponentResult
	var root string
	if request.target != "" {
		var err error
		root, err = resolveRuntimeRecoveryRoot()
		if err != nil {
			return result, err
		}
		writer, err := desktopupdate.BeginIndependentComponentWrite(ctx, root, paths.RuntimeRoot)
		if err != nil {
			return result, err
		}
		defer desktopupdate.ReleaseNativeLock(writer)
	}
	current, err := measureDesktopUpdateState(ctx, cfg, paths)
	if err != nil {
		return result, err
	}
	if current.Dirty {
		return result, updateStateFailure("data_dirty")
	}
	if expectedSize <= 0 || expectedSize > maxUpdateComponentCatalog || !componentDigestPattern.MatchString(expectedHash) {
		return result, updateStateFailure("component_update_incompatible")
	}
	artifact := desktopupdate.Artifact{SHA256: expectedHash, Size: expectedSize}
	data, err := readV1ComponentCatalog(file)
	if err != nil {
		return result, err
	}
	if err = verifyV1CatalogBytes(data, artifact); err != nil {
		return result, err
	}
	target, err := resolvePythonComponentCatalog(paths, data, true)
	if err != nil {
		return result, updateStateFailure("component_update_incompatible")
	}
	// Missing optional components are valid for a lightweight installation.
	// Exact comparison rejects removing or replacing anything already installed.
	components, err := measuredComponentsFromCatalog(paths, true, target)
	if err != nil || !reflect.DeepEqual(current.Components, components) {
		return result, updateStateFailure("component_update_incompatible")
	}
	if request.target != "" {
		written, err := saveV1ComponentHandoff(ctx, cfg, paths, root, request, artifact, current, target)
		if err != nil {
			return result, err
		}
		result.AttemptID = written.AttemptID
		result.PreviousAttemptID = written.PreviousAttemptID
	}
	result.Status = "compatible"
	result.DataProfile = current.DataProfile
	if request.prepare {
		result.Status = "prepared"
		result.ReleaseID = request.target
	}
	return result, nil
}

func (c *CLI) updateComponentsCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("update-components-check", flag.ContinueOnError)
	flags.SetOutput(c.errOut)
	options := addRuntimeFlags(flags)
	file := flags.String("components", "", "")
	digest := flags.String("sha256", "", "")
	size := flags.Int64("size", 0, "")
	prepare := flags.String("prepare-handoff", "", "prepare a verified attempt without authorizing installation")
	inspect := flags.Bool("inspect-handoff", false, "inspect source-side recovery eligibility")
	attempt := flags.String("attempt-id", "", "expected v1 handoff attempt")
	handoff := flags.String("handoff", "", "record a verified v1 target before native installation")
	packageFile := flags.String("package", "", "cached native update package")
	discard := flags.String("discard-handoff", "", "discard a handoff that did not reach native installation")
	if err := flags.Parse(args); err != nil {
		return err
	}
	var result updateComponentResult
	request := v1HandoffRequest{target: *handoff, packageFile: *packageFile, attempt: *attempt, prepare: *prepare != ""}
	if request.prepare {
		request.target = *prepare
	}
	cfg, paths, err := newRuntimeConfigWithOptions(options(), true)
	if err != nil {
		err = updateStateFailure("data_unknown")
	}
	readOrDiscard := *inspect || *discard != ""
	if len(flags.Args()) != 0 || (*prepare != "" && *handoff != "") || (*inspect && *discard != "") ||
		(request.target == "") != (*packageFile == "") || (*attempt != "" && (len(*attempt) != 32 || !componentDigestPattern.MatchString(*attempt+*attempt))) ||
		(readOrDiscard && (request.target != "" || *file != "" || *digest != "" || *size != 0)) ||
		(!readOrDiscard && (*file == "" || request.target == "" && *attempt != "")) || (*inspect && *attempt != "") {
		err = updateStateFailure("update_request_invalid")
	}
	if err == nil && (request.prepare || *inspect || *attempt != "" || *discard != "" || request.target != "" && os.Getppid() > 0) {
		var parent desktopupdate.ProcessIdentity
		parent, err = desktopupdate.IdentifyProcess(os.Getppid())
		request.actor = &parent
	}
	if err == nil {
		if readOrDiscard {
			result, err = inspectOrDiscardV1Handoff(ctx, cfg, paths, *discard, *attempt, request.actor)
		} else {
			result, err = checkUpdateComponents(ctx, cfg, paths, *file, *digest, *size, request)
		}
	}
	if err != nil {
		result = updateComponentResult{Status: "blocked", Reason: desktopupdate.ErrorCode(err)}
	}
	return json.NewEncoder(c.out).Encode(result)
}
