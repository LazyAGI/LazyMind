package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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
	if err := os.MkdirAll(filepath.Dir(cfg.ModeProfile.VectorStore.DBPath), 0o755); err != nil {
		return err
	}
	algorithm := NewAlgorithmServiceManager(m.runner)
	if err := algorithm.preparePython(ctx, paths, false); err != nil {
		return err
	}

	address := "127.0.0.1:" + strconv.Itoa(cfg.ModeProfile.VectorStore.Port)
	cmd := exec.CommandContext(ctx, paths.AlgorithmPython,
		"-m", "lazymind.local_milvus_lite",
		"--db-file", cfg.ModeProfile.VectorStore.DBPath,
		"--address", address,
	)
	cmd.Dir = paths.RepoRoot
	cmd.Env = append(os.Environ(), algorithmServiceEnv(cfg, paths, milvusLiteProcessName)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s failed: %w", milvusLiteProcessName, err)
	}
	if err := os.WriteFile(paths.MilvusLitePIDFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		_ = killAlgorithmProcess(cmd.Process)
		return err
	}

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- cmd.Wait()
	}()
	if err := waitForMilvusLiteReady(ctx, cfg.ModeProfile.VectorStore.Port, waitErr); err != nil {
		_ = killAlgorithmProcess(cmd.Process)
		_ = os.Remove(paths.MilvusLitePIDFile)
		return err
	}

	err := <-waitErr
	_ = os.Remove(paths.MilvusLitePIDFile)
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
	if err := signalProcessGroup(pid, syscall.SIGINT); err != nil {
		_ = proc.Signal(os.Interrupt)
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = signalProcessGroup(pid, syscall.SIGKILL)
			_ = proc.Kill()
			return ctx.Err()
		case <-deadline.C:
			_ = signalProcessGroup(pid, syscall.SIGKILL)
			_ = proc.Kill()
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
