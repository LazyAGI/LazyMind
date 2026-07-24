package source

import (
	"context"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

const (
	localFSConnectorType = "local_fs"
	localFSTargetType    = "local_path"

	agentCommandStartSource  = "start_source"
	agentCommandReloadSource = "reload_source"
	agentCommandStopSource   = "stop_source"
	agentCommandRootPathKey  = "root" + "_path"
)

func (e *DefaultEngine) queueLocalWatcherStarts(ctx context.Context, src store.Source, bindings []store.Binding) []JobError {
	var errors []JobError
	for _, binding := range bindings {
		if !localWatcherStartable(binding) {
			continue
		}
		if err := e.queueLocalWatcherCommand(ctx, src, binding, agentCommandStartSource, e.clock().UTC()); err != nil {
			errors = append(errors, localWatcherCommandError(binding, agentCommandStartSource, err))
		}
	}
	return errors
}

func (e *DefaultEngine) queueLocalWatcherStops(ctx context.Context, src store.Source, bindings []store.Binding) []JobError {
	var errors []JobError
	for _, binding := range bindings {
		if !localWatcherStoppable(binding) {
			continue
		}
		if err := e.queueLocalWatcherCommand(ctx, src, binding, agentCommandStopSource, e.clock().UTC()); err != nil {
			errors = append(errors, localWatcherCommandError(binding, agentCommandStopSource, err))
		}
	}
	return errors
}

func (e *DefaultEngine) queueLocalWatcherTransition(ctx context.Context, src store.Source, current, updated store.Binding) []JobError {
	currentStartable := localWatcherStartable(current)
	updatedStartable := localWatcherStartable(updated)
	now := e.clock().UTC()
	var errors []JobError
	switch {
	case currentStartable && !updatedStartable:
		if err := e.queueLocalWatcherCommand(ctx, src, current, agentCommandStopSource, now); err != nil {
			errors = append(errors, localWatcherCommandError(current, agentCommandStopSource, err))
		}
	case !currentStartable && updatedStartable:
		if err := e.queueLocalWatcherCommand(ctx, src, updated, agentCommandStartSource, now); err != nil {
			errors = append(errors, localWatcherCommandError(updated, agentCommandStartSource, err))
		}
	case currentStartable && updatedStartable && localWatcherRuntimeChanged(current, updated):
		if current.AgentID == updated.AgentID {
			if err := e.queueLocalWatcherCommand(ctx, src, updated, agentCommandReloadSource, now); err != nil {
				errors = append(errors, localWatcherCommandError(updated, agentCommandReloadSource, err))
			}
			return errors
		}
		if err := e.queueLocalWatcherCommand(ctx, src, current, agentCommandStopSource, now); err != nil {
			errors = append(errors, localWatcherCommandError(current, agentCommandStopSource, err))
		}
		if err := e.queueLocalWatcherCommand(ctx, src, updated, agentCommandStartSource, now.Add(time.Nanosecond)); err != nil {
			errors = append(errors, localWatcherCommandError(updated, agentCommandStartSource, err))
		}
	}
	return errors
}

func (e *DefaultEngine) queueLocalWatcherCommand(ctx context.Context, src store.Source, binding store.Binding, commandType string, now time.Time) error {
	command := store.AgentCommand{
		CommandID:   numericCommandID(e.newID("agent-command")),
		AgentID:     binding.AgentID,
		CommandType: commandType,
		Payload: store.JSON{
			"type":      commandType,
			"tenant_id": src.TenantID,
			"source_id": binding.SourceID,
		},
		Status:    "PENDING",
		LastError: store.JSON{},
		Result:    store.JSON{},
		CreatedAt: now,
	}
	if commandType != agentCommandStopSource {
		command.Payload[agentCommandRootPathKey] = binding.TargetRef
		command.Payload["skip_initial_scan"] = true
	}
	return mapStoreError(e.repo.CreateAgentCommand(ctx, command))
}

func localWatcherStartable(binding store.Binding) bool {
	return localWatcherStoppable(binding) && binding.Status == BindingStatusActive && strings.TrimSpace(binding.TargetRef) != ""
}

func localWatcherStoppable(binding store.Binding) bool {
	return binding.ConnectorType == localFSConnectorType &&
		binding.TargetType == localFSTargetType &&
		strings.TrimSpace(binding.AgentID) != ""
}

func localWatcherRuntimeChanged(current, updated store.Binding) bool {
	return current.ConnectorType != updated.ConnectorType ||
		current.TargetType != updated.TargetType ||
		current.AgentID != updated.AgentID ||
		current.TargetRef != updated.TargetRef
}

func localWatcherCommandError(binding store.Binding, commandType string, err error) JobError {
	return JobError{
		Code:    string(ErrCodeInternal),
		Message: err.Error(),
		Details: map[string]any{
			"binding_id":   binding.BindingID,
			"agent_id":     binding.AgentID,
			"command_type": commandType,
		},
	}
}

func numericCommandID(seed string) string {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(seed))
	value := hash.Sum64() & ((uint64(1) << 63) - 1)
	if value == 0 {
		value = 1
	}
	return strconv.FormatUint(value, 10)
}
