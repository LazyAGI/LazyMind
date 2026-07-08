package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ResetScope string

const (
	ResetScopeKB  ResetScope = "kb"
	ResetScopeAll ResetScope = "all"
)

func parseResetScope(raw string) (ResetScope, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(ResetScopeKB):
		return ResetScopeKB, nil
	case string(ResetScopeAll):
		return ResetScopeAll, nil
	default:
		return "", fmt.Errorf("--scope must be kb or all")
	}
}

func (m *RuntimeManager) Reset(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths, scope ResetScope) error {
	if scope != ResetScopeKB && scope != ResetScopeAll {
		return fmt.Errorf("unsupported reset scope %q", scope)
	}
	m.progressf("stopping Local Runtime before %s reset", scope)
	if err := m.Down(ctx, cfg, paths); err != nil {
		m.progressf("Local Runtime down failed during reset; continuing cleanup: %v", err)
	}
	if err := m.resetKBLocalState(ctx, paths); err != nil {
		return err
	}
	if scope == ResetScopeAll {
		if err := m.resetAllLocalState(ctx, paths); err != nil {
			return err
		}
		m.progressf("local persistent data cleared")
		return nil
	}
	m.progressf("local KB data cleared")
	return nil
}

func (m *RuntimeManager) RunServiceAction(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths, service string, action string) error {
	service = strings.ToLower(strings.TrimSpace(service))
	action = strings.ToLower(strings.TrimSpace(action))
	if service != fileWatcherProcessName {
		return fmt.Errorf("unsupported service %q", service)
	}
	switch action {
	case "build":
		if err := paths.EnsureAllDirs(); err != nil {
			return err
		}
		return m.fileWatcher.build(ctx, paths)
	case "start":
		return m.fileWatcher.Run(ctx, cfg, paths)
	case "stop":
		return m.fileWatcher.Down(ctx, paths)
	default:
		return fmt.Errorf("unsupported service action %q", action)
	}
}

func (m *RuntimeManager) resetKBLocalState(ctx context.Context, paths RuntimePaths) error {
	if err := m.resetLocalSQLiteKBState(ctx, paths); err != nil {
		return err
	}
	for _, path := range localKBResetPaths(paths) {
		if err := m.removeLocalPath(ctx, paths.RepoRoot, path); err != nil {
			return err
		}
	}
	return nil
}

func (m *RuntimeManager) resetAllLocalState(ctx context.Context, paths RuntimePaths) error {
	for _, path := range localAllResetPaths(paths) {
		if err := m.removeLocalPath(ctx, paths.RepoRoot, path); err != nil {
			return err
		}
	}
	return nil
}

func localKBResetPaths(paths RuntimePaths) []string {
	return []string{
		paths.UploadRoot,
		filepath.Join(paths.AlgorithmHome, "sqlite"),
		paths.ScanControlPlaneTempDir,
		filepath.Join(paths.FileWatcherBaseRoot, "staging"),
		filepath.Join(paths.FileWatcherBaseRoot, "snapshots"),
		filepath.Dir(paths.MilvusLiteDBPath),
	}
}

func localAllResetPaths(paths RuntimePaths) []string {
	return []string{
		paths.DataDir,
		paths.GeneratedDir,
		paths.LogsDir,
		paths.RunDir,
		paths.StateDir,
		filepath.Join(paths.RuntimeRoot, "tmp"),
	}
}

func (m *RuntimeManager) removeLocalPath(ctx context.Context, repoRoot string, path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		m.progressf("  skip %s (not found)", displayPath(repoRoot, path))
		return nil
	}
	m.progressf("  removing %s", displayPath(repoRoot, path))
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove %s failed: %w", displayPath(repoRoot, path), err)
	}
	return nil
}

func (m *RuntimeManager) resetLocalSQLiteKBState(ctx context.Context, paths RuntimePaths) error {
	python, err := ensureLocalPythonRuntime(ctx, m.runner, paths, envText(localPythonVersionEnvVar, defaultLocalPythonVersion))
	if err != nil {
		return err
	}
	script := `
import os
import sqlite3
import sys

core_db, lazyllm_db = sys.argv[1], sys.argv[2]

core_statements = [
    "DELETE FROM tasks",
    "DELETE FROM upload_sessions",
    "DELETE FROM uploaded_files",
    "DELETE FROM documents",
    "DELETE FROM acl_kbs",
    "DELETE FROM default_datasets",
    "DELETE FROM datasets",
    "UPDATE conversations SET search_config = '{}' WHERE search_config IS NOT NULL AND search_config <> '{}'",
]

lazyllm_tables = [
    "lazyllm_doc_node_group_status",
    "lazyllm_doc_parse_state",
    "lazyllm_kb_algorithm",
    "lazyllm_kb_documents",
    "lazyllm_knowledge_bases",
    "lazyllm_doc_path_locks",
    "lazyllm_documents",
    "lazyllm_doc_service_tasks",
    "lazyllm_callback_records",
    "lazyllm_idempotency_records",
    "lazyllm_node_group",
    "lazyllm_algorithm",
    "lazyllm_waiting_task_queue",
    "lazyllm_finished_task_queue",
]

def run(path, statements):
    if not path or not os.path.exists(path):
        return
    conn = sqlite3.connect(path)
    try:
        for sql in statements:
            try:
                conn.execute(sql)
            except sqlite3.OperationalError as exc:
                message = str(exc).lower()
                if "no such table" not in message and "no such column" not in message:
                    raise
        conn.commit()
    finally:
        conn.close()

run(core_db, core_statements)
run(lazyllm_db, ["DROP TABLE IF EXISTS " + table for table in lazyllm_tables])
`
	res, err := m.runner.Run(ctx, Command{
		Name: python,
		Args: []string{"-c", script, paths.CoreDBPath, paths.LazyLLMDBPath},
		Dir:  paths.RepoRoot,
		Env:  pythonRuntimeEnv(paths),
	})
	if err != nil {
		return fmt.Errorf("reset local SQLite KB state failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func displayPath(repoRoot string, path string) string {
	if rel, err := filepath.Rel(repoRoot, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return path
}
