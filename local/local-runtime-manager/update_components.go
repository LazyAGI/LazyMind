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

func checkUpdateComponents(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths, file, expectedHash string, expectedSize int64) (string, error) {
	current, err := measureDesktopUpdateState(ctx, cfg, paths)
	if err != nil {
		return "", err
	}
	if current.Dirty {
		return "", updateStateFailure("data_dirty")
	}
	if expectedSize <= 0 || expectedSize > maxUpdateComponentCatalog || !componentDigestPattern.MatchString(expectedHash) {
		return "", updateStateFailure("component_update_incompatible")
	}
	info, err := os.Lstat(file)
	if err != nil || !info.Mode().IsRegular() {
		return "", updateStateFailure("component_update_incompatible")
	}
	if info.Size() != expectedSize {
		return "", updateStateFailure("artifact_size")
	}
	f, err := os.Open(file)
	if err != nil {
		return "", updateStateFailure("component_update_incompatible")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxUpdateComponentCatalog+1))
	if err != nil {
		return "", updateStateFailure("component_update_incompatible")
	}
	if int64(len(data)) != expectedSize {
		return "", updateStateFailure("artifact_size")
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != expectedHash {
		return "", updateStateFailure("artifact_hash")
	}
	target, err := resolvePythonComponentCatalog(paths, data, true)
	if err != nil {
		return "", updateStateFailure("component_update_incompatible")
	}
	// Missing optional components are valid for a lightweight installation.
	// Exact comparison rejects removing or replacing anything already installed.
	components, err := measuredComponentsFromCatalog(paths, true, target)
	if err != nil || !reflect.DeepEqual(current.Components, components) {
		return "", updateStateFailure("component_update_incompatible")
	}
	return current.DataProfile, nil
}

func (c *CLI) updateComponentsCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("update-components-check", flag.ContinueOnError)
	flags.SetOutput(c.errOut)
	options := addRuntimeFlags(flags)
	file := flags.String("components", "", "")
	digest := flags.String("sha256", "", "")
	size := flags.Int64("size", 0, "")
	if err := flags.Parse(args); err != nil {
		return err
	}
	var result struct {
		Status      string `json:"status"`
		Reason      string `json:"reason,omitempty"`
		DataProfile string `json:"dataProfile,omitempty"`
	}
	cfg, paths, err := newRuntimeConfigWithOptions(options(), true)
	if err != nil {
		err = updateStateFailure("data_unknown")
	}
	if len(flags.Args()) != 0 || *file == "" {
		err = updateStateFailure("component_update_incompatible")
	}
	if err == nil {
		result.DataProfile, err = checkUpdateComponents(ctx, cfg, paths, *file, *digest, *size)
	}
	if err != nil {
		result.Status, result.Reason = "blocked", desktopupdate.ErrorCode(err)
	} else {
		result.Status = "compatible"
	}
	return json.NewEncoder(c.out).Encode(result)
}
