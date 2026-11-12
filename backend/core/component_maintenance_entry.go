package main

import (
	"bufio"
	"context"
	"encoding/json"
	"lazymind/core/systemdeps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

var resolveComponentMaintenanceRoot = systemdeps.ComponentUpdaterRoot

// Dispatch before ordinary Core configuration, credentials, database or jobs.
func runComponentMaintenanceEntry() bool {
	if len(os.Args) < 2 || os.Args[1] != "--component-maintenance" {
		return false
	}
	emit := func(reason string) {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "blocked", "reason": reason})
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 4097)
	if !scanner.Scan() {
		emit("update_request_invalid")
		return true
	}
	raw := append([]byte(nil), scanner.Bytes()...)
	err := scanner.Err()
	var request struct{ TransactionID, ID string }
	if len(os.Args) != 3 || (os.Args[2] != "stage" && os.Args[2] != "activate" && os.Args[2] != "independent") || err != nil || len(raw) > 4096 || json.Unmarshal(raw, &request) != nil || (os.Args[2] != "independent" && len(request.TransactionID) != 32) || (os.Args[2] == "independent" && request.ID != "rag") {
		emit("update_request_invalid")
		return true
	}
	root, err := resolveComponentMaintenanceRoot()
	if err != nil {
		emit("update_record_read")
		return true
	}
	if os.Args[2] == "independent" {
		resources, runtimeRoot := os.Getenv("LAZYMIND_RUNTIME_RESOURCES_ROOT"), os.Getenv("LAZYMIND_RUNTIME_ROOT")
		if !filepath.IsAbs(resources) || !filepath.IsAbs(runtimeRoot) {
			emit("update_request_invalid")
			return true
		}
		python := filepath.Join(resources, "deps", "python", "algorithm", "bin", "python")
		if runtime.GOOS == "windows" {
			python = filepath.Join(resources, "deps", "python", "algorithm", "Scripts", "python.exe")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
		defer cancel()
		go func() { scanner.Scan(); cancel() }()
		result, err := systemdeps.RunIndependentPythonComponent(ctx, systemdeps.PythonComponentMaintenanceContext{RuntimeRoot: runtimeRoot, ResourcesRoot: resources, UpdaterRoot: root, Python: python, AlgorithmSource: filepath.Join(resources, "app", "algorithm")})
		if err != nil {
			emit("component_install_failed")
		} else {
			_, _ = os.Stdout.Write(result)
		}
		return true
	}
	filename := filepath.Join(root, "native-v2", "active.json")
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 512*1024 {
		emit("update_record_read")
		return true
	}
	recordBytes, err := os.ReadFile(filename)
	var record struct{ RuntimeRoot, InstallationRoot, TransactionID string }
	if err != nil || json.Unmarshal(recordBytes, &record) != nil || record.TransactionID != request.TransactionID || !filepath.IsAbs(record.RuntimeRoot) || !filepath.IsAbs(record.InstallationRoot) {
		emit("update_record_read")
		return true
	}
	helperName := "lazymind-update-helper"
	python := filepath.Join(record.InstallationRoot, "deps", "python", "algorithm", "bin", "python")
	if runtime.GOOS == "windows" {
		helperName += ".exe"
		python = filepath.Join(record.InstallationRoot, "deps", "python", "algorithm", "Scripts", "python.exe")
	}
	// Allow the helper's bounded 60-second signature verification to finish.
	identityCtx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	command := exec.CommandContext(identityCtx, filepath.Join(record.InstallationRoot, "bin", helperName), "verify-identity")
	var identity struct {
		Status string
		Config struct{ ReleaseID string }
	}
	output, err := command.Output()
	cancel()
	if err != nil || len(output) > 1024*1024 || json.Unmarshal(output, &identity) != nil || identity.Status != "enabled" || identity.Config.ReleaseID == "" {
		emit("update_transaction_conflict")
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	go func() { scanner.Scan(); cancel() }()
	result, err := systemdeps.RunPythonComponentMaintenance(ctx, os.Args[2], raw, systemdeps.PythonComponentMaintenanceContext{RuntimeRoot: record.RuntimeRoot, ResourcesRoot: record.InstallationRoot, UpdaterRoot: root, ReleaseID: identity.Config.ReleaseID, Python: python, AlgorithmSource: filepath.Join(record.InstallationRoot, "app", "algorithm")})
	if err != nil {
		emit("component_maintenance_failed")
	} else {
		_, _ = os.Stdout.Write(result)
	}
	return true
}
