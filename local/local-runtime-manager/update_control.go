package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"strconv"
	"time"
)

type updateWorkState struct {
	Active int64  `json:"active"`
	Paused bool   `json:"paused"`
	Epoch  uint64 `json:"epoch"`
}

func desktopUpdateRuntime(cfg RuntimeConfig, paths RuntimePaths) (RuntimeConfig, RuntimeState, error) {
	s, err := readRuntimeState(paths.StateFile)
	if err != nil || cfg.Profile != "desktop" || s.Profile != "desktop" || s.OverallStatus != "ready" || s.Config.MaintenanceMode != "" || validateRequestedRuntimeOwner(cfg) != nil || activeRuntimeOwnershipError(s, cfg) != nil || !sameRuntimePath(s.RuntimeRoot, paths.RuntimeRoot) {
		return cfg, s, updateStateFailure("runtime_owner_mismatch")
	}
	return applyStateConfig(cfg, s), s, nil
}

func requestDesktopUpdate(ctx context.Context, client *http.Client, port int, token, action string, epoch uint64) (updateWorkState, error) {
	var result updateWorkState
	if port <= 0 || port > 65535 || token == "" {
		return result, updateStateFailure("task_state_unknown")
	}
	data, _ := json.Marshal(map[string]any{"action": action, "epoch": epoch})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(port)+"/internal/desktop-update", bytes.NewReader(data))
	if err != nil {
		return result, updateStateFailure("task_state_unknown")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-LazyMind-Internal-Token", token)
	res, err := client.Do(req)
	if err != nil {
		return result, updateStateFailure("task_state_unknown")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return result, updateStateFailure("task_state_unknown")
	}
	var wire struct {
		Active *int64  `json:"active"`
		Paused *bool   `json:"paused"`
		Epoch  *uint64 `json:"epoch"`
	}
	d := json.NewDecoder(io.LimitReader(res.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(&wire) != nil || d.Decode(new(any)) != io.EOF || wire.Active == nil || *wire.Active < 0 || wire.Paused == nil || wire.Epoch == nil || *wire.Paused != (*wire.Epoch != 0) {
		return result, updateStateFailure("task_state_unknown")
	}
	result = updateWorkState{*wire.Active, *wire.Paused, *wire.Epoch}
	if action == "pause" && (!result.Paused || result.Epoch != epoch) || action == "resume" && result.Paused {
		return updateWorkState{}, updateStateFailure("task_state_unknown")
	}
	return result, nil
}

func controlDesktopUpdate(ctx context.Context, cfg RuntimeConfig, action string, epoch uint64) (updateWorkState, error) {
	client := &http.Client{Timeout: 7 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	ports := []int{cfg.LocalProxy.CoreHostPort}
	if buildRuntimeProcessPlan(cfg).includes(scanControlPlaneProcessName) {
		ports = append([]int{cfg.LocalProxy.ScanHostPort}, ports...)
	}
	token := internalServiceToken()
	result := updateWorkState{Paused: action == "pause", Epoch: epoch}
	var firstErr error
	for _, port := range ports {
		s, err := requestDesktopUpdate(ctx, client, port, token, action, epoch)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		result.Active += s.Active
		if action == "status" && s.Paused {
			firstErr = updateStateFailure("update_already_preparing")
		}
	}
	if firstErr != nil && action == "pause" {
		// Both peers are retried on resume: the failed response may have followed
		// a successful pause. Never infer admission from a lost response.
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, port := range ports {
			_, _ = requestDesktopUpdate(cleanup, client, port, token, "resume", epoch)
		}
	}
	if action != "pause" {
		result.Paused = false
		result.Epoch = 0
	}
	return result, firstErr
}

func (c *CLI) desktopUpdateCommand(ctx context.Context, manager *RuntimeManager, args []string) error {
	flags := flag.NewFlagSet("update-control", flag.ContinueOnError)
	flags.SetOutput(c.errOut)
	opts := addRuntimeFlags(flags)
	action := flags.String("action", "status", "")
	epoch := flags.Uint64("epoch", 0, "")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 || (*action != "status" && *action != "pause" && *action != "resume" && *action != "stop") || (*action != "status" && *epoch == 0) {
		return updateStateFailure("update_control_invalid")
	}
	cfg, paths, err := newRuntimeConfigWithOptions(opts(), true)
	if err != nil {
		return updateStateFailure("task_state_unknown")
	}
	cfg, persisted, err := desktopUpdateRuntime(cfg, paths)
	if err != nil {
		return err
	}
	if *action != "stop" {
		result, err := controlDesktopUpdate(ctx, cfg, *action, *epoch)
		if err != nil {
			return err
		}
		return json.NewEncoder(c.out).Encode(result)
	}
	return withRuntimeUpdateLease(ctx, cfg, paths, func() error {
		work, err := controlDesktopUpdate(ctx, cfg, "pause", *epoch)
		if err != nil {
			return err
		}
		if work.Active != 0 {
			return updateStateFailure("tasks_active")
		}
		// Use the regular supervisor shutdown, without Down's emergency cleanup
		// fallback. A failed stop cannot authorize native installation.
		stopCtx, cancel := context.WithTimeout(ctx, 75*time.Second)
		defer cancel()
		if err := manager.processCompose.Down(stopCtx, cfg, paths, io.Discard, io.Discard); err != nil {
			return updateStateFailure("runtime_stop_failed")
		}
		for {
			records, err := discoverLocalRuntimeProcessesChecked(paths, cfg, manager.processScanner)
			if err != nil {
				return updateStateFailure("runtime_stop_failed")
			}
			if len(records) == 0 && !manager.probeAPI(cfg.ProcessComposePort, 500*time.Millisecond) {
				break
			}
			select {
			case <-stopCtx.Done():
				return updateStateFailure("runtime_stop_failed")
			case <-time.After(200 * time.Millisecond):
			}
		}
		persisted = newStateWithServiceStatus(persisted, cfg, "stopped")
		persisted.OverallStatus = "stopped"
		persisted.Diagnostic = nil
		persisted.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if writeRuntimeState(paths.StateFile, persisted) != nil {
			return updateStateFailure("runtime_stop_failed")
		}
		return json.NewEncoder(c.out).Encode(map[string]string{"status": "stopped"})
	})
}
