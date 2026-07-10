package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type MilvusLiteManager struct {
	runner CommandRunner
}

func NewMilvusLiteManager(r CommandRunner) *MilvusLiteManager {
	return &MilvusLiteManager{runner: r}
}

func (m *MilvusLiteManager) Run(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if !cfg.ModeProfile.VectorStore.ManagedProcess {
		return nil
	}
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.ModeProfile.VectorStore.DBPath, 0o755); err != nil {
		return err
	}
	algorithm := NewAlgorithmServiceManager(m.runner)
	if err := algorithm.preparePython(ctx, cfg, paths, false); err != nil {
		return err
	}

	milvusLite := venvExecutable(paths.AlgorithmVenv, "milvus-lite")
	cmd := exec.CommandContext(ctx, milvusLite,
		"server",
		"--data-dir", cfg.ModeProfile.VectorStore.DBPath,
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(cfg.ModeProfile.VectorStore.Port),
	)
	cmd.Dir = paths.RepoRoot
	cmd.Env = append(os.Environ(), algorithmServiceEnv(cfg, paths, milvusLiteProcessName)...)
	configureChildProcess(cmd, false)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s failed: %w", milvusLiteProcessName, err)
	}
	releaseJob, err := attachManagedProcess(paths, milvusLiteProcessName, cmd.Process)
	if err != nil {
		_ = killAlgorithmProcess(cmd.Process)
		return fmt.Errorf("attach %s process containment failed: %w", milvusLiteProcessName, err)
	}
	defer releaseJob()
	if err := os.WriteFile(paths.MilvusLitePIDFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		_ = killAlgorithmProcess(cmd.Process)
		return err
	}
	registerLocalProcess(paths, milvusLiteProcessName, cmd.Process.Pid, []int{cfg.ModeProfile.VectorStore.Port}, append([]string{milvusLite}, cmd.Args...))

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- cmd.Wait()
	}()
	if err := waitForMilvusLiteReady(ctx, cfg.ModeProfile.VectorStore.Port, waitErr); err != nil {
		_ = killAlgorithmProcess(cmd.Process)
		_ = os.Remove(paths.MilvusLitePIDFile)
		unregisterLocalProcess(paths, milvusLiteProcessName, cmd.Process.Pid)
		return err
	}

	err = <-waitErr
	_ = os.Remove(paths.MilvusLitePIDFile)
	unregisterLocalProcess(paths, milvusLiteProcessName, cmd.Process.Pid)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s exited: %w", milvusLiteProcessName, err)
	}
	return nil
}

func (m *MilvusLiteManager) Down(ctx context.Context, paths RuntimePaths) error {
	pid, err := readPIDFile(paths.MilvusLitePIDFile)
	if err != nil {
		return err
	}
	if pid <= 0 {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(paths.MilvusLitePIDFile)
		return nil
	}
	if err := interruptProcess(pid); err != nil {
		_ = proc.Signal(os.Interrupt)
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = forceStopManagedProcess(paths, milvusLiteProcessName, pid)
			return ctx.Err()
		case <-deadline.C:
			_ = forceStopManagedProcess(paths, milvusLiteProcessName, pid)
			_ = os.Remove(paths.MilvusLitePIDFile)
			return nil
		case <-ticker.C:
			if !processAlive(pid) {
				_ = os.Remove(paths.MilvusLitePIDFile)
				return nil
			}
		}
	}
}

func readPIDFile(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		_ = os.Remove(path)
		return 0, nil
	}
	return pid, nil
}

func waitForMilvusLiteReady(ctx context.Context, port int, waitErr <-chan error) error {
	deadline := time.NewTimer(5 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if tcpOK(ctx, "127.0.0.1", port, time.Second) {
			return nil
		}
		select {
		case err := <-waitErr:
			if err == nil {
				return fmt.Errorf("%s exited before becoming ready", milvusLiteProcessName)
			}
			return fmt.Errorf("%s exited before becoming ready: %w", milvusLiteProcessName, err)
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for %s at 127.0.0.1:%d", milvusLiteProcessName, port)
		case <-ticker.C:
		}
	}
}
