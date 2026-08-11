package source

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	agentCommandLeaseTTL    = 2 * time.Minute
	agentCommandMaxAttempts = 10
)

var agentLifecycleCommandTypes = []string{"start_source", "reload_source", "stop_source"}

func (r *SQLRepository) GetAgent(ctx context.Context, agentID string) (Agent, error) {
	db := r.ormDB(ctx)
	if db == nil {
		return Agent{}, NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	var row ormAgent
	err := db.Where("agent_id = ?", agentID).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Agent{}, NewStoreError(ErrCodeAgentNotFound, "agent not found")
		}
		return Agent{}, mapSQLConstraint(err)
	}
	return agentFromORM(row), nil
}

func (r *SQLRepository) UpsertAgent(ctx context.Context, agent Agent) error {
	db := r.ormDB(ctx)
	if db == nil {
		return NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	model := agentToORM(agent)
	return mapSQLConstraint(db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "agent_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"tenant_id":           agent.TenantID,
			"hostname":            agent.Hostname,
			"version":             agent.Version,
			"status":              agent.Status,
			"listen_addr":         agent.ListenAddr,
			"last_heartbeat_at":   agent.LastHeartbeatAt,
			"active_source_count": agent.ActiveSourceCount,
			"active_watch_count":  agent.ActiveWatchCount,
			"active_task_count":   agent.ActiveTaskCount,
			"updated_at":          agent.UpdatedAt,
		}),
	}).Create(&model).Error)
}

func (r *SQLRepository) ListWatchBindingsForAgentEvent(ctx context.Context, sourceID, agentID string) ([]Binding, error) {
	db := r.ormDB(ctx)
	if db == nil {
		return nil, NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	var rows []ormBinding
	err := db.Where("source_id = ? AND agent_id = ? AND connector_type = ? AND target_type = ? AND sync_mode IN ? AND status = ?",
		sourceID, agentID, "local_fs", "local_path", []string{"manual", "scheduled", "watch"}, "ACTIVE",
	).
		Order("binding_id").
		Find(&rows).Error
	if err != nil {
		return nil, mapSQLConstraint(err)
	}
	bindings := make([]Binding, 0, len(rows))
	for _, row := range rows {
		bindings = append(bindings, bindingFromORM(row))
	}
	return bindings, nil
}

func (r *SQLRepository) ListLocalWatcherBindingsForAgent(ctx context.Context, agentID string) ([]Binding, error) {
	db := r.ormDB(ctx)
	if db == nil {
		return nil, NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	var rows []ormBinding
	err := db.Where("agent_id = ? AND connector_type = ? AND target_type = ? AND sync_mode IN ? AND status = ? AND target_ref <> ?",
		agentID, "local_fs", "local_path", []string{"manual", "scheduled", "watch"}, "ACTIVE", "",
	).
		Order("source_id, binding_id").
		Find(&rows).Error
	if err != nil {
		return nil, mapSQLConstraint(err)
	}
	bindings := make([]Binding, 0, len(rows))
	for _, row := range rows {
		bindings = append(bindings, bindingFromORM(row))
	}
	return bindings, nil
}

func (r *SQLRepository) RecoverLocalWatchers(ctx context.Context, now time.Time) (int, error) {
	db := r.ormDB(ctx)
	if db == nil {
		return 0, NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	var rows []ormBinding
	if err := db.Where("connector_type = ? AND target_type = ? AND sync_mode IN ? AND status = ? AND agent_id <> ? AND target_ref <> ?",
		"local_fs", "local_path", []string{"manual", "scheduled", "watch"}, "ACTIVE", "", "",
	).Order("agent_id, source_id, binding_id").Find(&rows).Error; err != nil {
		return 0, mapSQLConstraint(err)
	}
	recovered := 0
	for _, row := range rows {
		binding := bindingFromORM(row)
		source, err := r.GetSource(ctx, binding.SourceID)
		if err != nil {
			return recovered, err
		}
		if err := r.CreateAgentCommand(ctx, AgentCommand{
			CommandID:       WatcherCommandID(binding.AgentID, binding, "start_source"),
			AgentID:         binding.AgentID,
			QueueGeneration: AgentCommandQueueGeneration,
			CommandType:     "start_source",
			Payload: JSON{
				"type":              "start_source",
				"tenant_id":         source.TenantID,
				"source_id":         binding.SourceID,
				"binding_id":        binding.BindingID,
				"root" + "_path":    binding.TargetRef,
				"skip_initial_scan": true,
			},
			Status:    "PENDING",
			LastError: JSON{},
			Result:    JSON{},
			CreatedAt: now,
		}); err != nil {
			return recovered, err
		}
		if _, err := r.EnqueueBindingReconcile(ctx, ReconcileRequest{
			SourceID: binding.SourceID, BindingID: binding.BindingID,
			RequestID: WatcherRecoveryRequestID(binding, now), RunAt: now.Add(30 * time.Second),
		}); err != nil {
			return recovered, err
		}
		recovered++
	}
	return recovered, nil
}

func (r *SQLRepository) CreateAgentCommand(ctx context.Context, command AgentCommand) error {
	db := r.ormDB(ctx)
	if db == nil {
		return NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	if command.Status == "" {
		command.Status = "PENDING"
	}
	if command.QueueGeneration == 0 {
		command.QueueGeneration = AgentCommandQueueGeneration
	}
	if command.Payload == nil {
		command.Payload = JSON{}
	}
	if command.LastError == nil {
		command.LastError = JSON{}
	}
	if command.Result == nil {
		command.Result = JSON{}
	}
	model := agentCommandToORM(command)
	return mapSQLConstraint(db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "command_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"agent_id":         model.AgentID,
			"queue_generation": model.QueueGeneration,
			"command_type":     model.CommandType,
			"payload_json":     model.Payload,
			"status":           model.Status,
			"attempt_count":    int64(0),
			"next_retry_at":    nil,
			"acked_at":         nil,
			"last_error":       model.LastError,
			"result_json":      model.Result,
			"created_at":       model.CreatedAt,
			"dispatched_at":    nil,
		}),
	}).Create(model).Error)
}

func (r *SQLRepository) ListPendingAgentCommands(ctx context.Context, agentID string, now time.Time, limit int) ([]AgentCommand, error) {
	if limit <= 0 {
		limit = 50
	}
	var commands []AgentCommand
	err := r.withORMTx(ctx, func(tx *gorm.DB) error {
		var rows []ormAgentCommand
		leaseExpiredAt := now.Add(-agentCommandLeaseTTL)
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("agent_id = ? AND (queue_generation = ? OR (queue_generation < ? AND command_type NOT IN ?)) AND attempt_count < ?",
				agentID, AgentCommandQueueGeneration, AgentCommandQueueGeneration, agentLifecycleCommandTypes, agentCommandMaxAttempts).
			Where("(status = ? AND (next_retry_at IS NULL OR next_retry_at <= ?)) OR (status = ? AND command_type IN ? AND dispatched_at <= ?)",
				"PENDING", now, "DISPATCHED", agentLifecycleCommandTypes, leaseExpiredAt).
			Order("created_at, command_id").
			Limit(limit).
			Find(&rows).Error; err != nil {
			return err
		}
		commands = make([]AgentCommand, 0, len(rows))
		for _, row := range rows {
			command := agentCommandFromORM(row)
			commands = append(commands, command)
			previousStatus := row.Status
			previousAttemptCount := row.AttemptCount
			row.Status = "DISPATCHED"
			row.AttemptCount++
			row.DispatchedAt = &now
			if err := tx.Model(&ormAgentCommand{}).
				Where("command_id = ? AND status = ? AND attempt_count = ?", row.CommandID, previousStatus, previousAttemptCount).
				Updates(map[string]any{
					"status":        row.Status,
					"attempt_count": row.AttemptCount,
					"dispatched_at": row.DispatchedAt,
				}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return commands, err
}

func (r *SQLRepository) MaintainAgentCommands(ctx context.Context, now time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 5000
	}
	db := r.ormDB(ctx)
	if db == nil {
		return 0, NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	if err := db.Model(&ormAgentCommand{}).
		Where("queue_generation = ? AND status = ? AND attempt_count >= ? AND dispatched_at <= ?", AgentCommandQueueGeneration, "DISPATCHED", agentCommandMaxAttempts, now.Add(-agentCommandLeaseTTL)).
		Updates(map[string]any{"status": "FAILED", "acked_at": now, "last_error": JSON{"error": "command acknowledgement retry limit exceeded"}}).Error; err != nil {
		return 0, mapSQLConstraint(err)
	}
	result := db.Exec(`DELETE FROM agent_commands WHERE command_id IN (
		SELECT command_id FROM agent_commands
			WHERE (queue_generation < ? AND command_type IN ?)
				OR (status = 'ACKED' AND acked_at < ?)
				OR (status = 'FAILED' AND acked_at < ?)
			LIMIT ?
	)`, AgentCommandQueueGeneration, agentLifecycleCommandTypes, now.Add(-7*24*time.Hour), now.Add(-30*24*time.Hour), limit)
	if result.Error != nil {
		return 0, mapSQLConstraint(result.Error)
	}
	return result.RowsAffected, nil
}

func (r *SQLRepository) AckAgentCommand(ctx context.Context, ack AgentCommandAck) error {
	return r.withORMTx(ctx, func(tx *gorm.DB) error {
		status := "FAILED"
		lastError := JSON{}
		if ack.Success {
			status = "ACKED"
		} else {
			lastError = JSON{"error": ack.Error}
		}
		result := tx.Model(&ormAgentCommand{}).
			Where("agent_id = ? AND command_id = ?", ack.AgentID, ack.CommandID).
			Updates(map[string]any{
				"status":      status,
				"acked_at":    ack.AckedAt,
				"last_error":  lastError,
				"result_json": ack.Result,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return NewStoreError(ErrCodeNotFound, "agent command not found")
		}
		return nil
	})
}

func agentFromORM(row ormAgent) Agent {
	return Agent{
		AgentID:           row.AgentID,
		TenantID:          row.TenantID,
		Hostname:          row.Hostname,
		Version:           row.Version,
		Status:            row.Status,
		ListenAddr:        row.ListenAddr,
		LastHeartbeatAt:   row.LastHeartbeatAt,
		ActiveSourceCount: row.ActiveSourceCount,
		ActiveWatchCount:  row.ActiveWatchCount,
		ActiveTaskCount:   row.ActiveTaskCount,
		UpdatedAt:         row.UpdatedAt,
	}
}

func agentToORM(agent Agent) ormAgent {
	return ormAgent{
		AgentID:           agent.AgentID,
		TenantID:          agent.TenantID,
		Hostname:          agent.Hostname,
		Version:           agent.Version,
		Status:            agent.Status,
		ListenAddr:        agent.ListenAddr,
		LastHeartbeatAt:   agent.LastHeartbeatAt,
		ActiveSourceCount: agent.ActiveSourceCount,
		ActiveWatchCount:  agent.ActiveWatchCount,
		ActiveTaskCount:   agent.ActiveTaskCount,
		UpdatedAt:         agent.UpdatedAt,
	}
}

func agentCommandFromORM(row ormAgentCommand) AgentCommand {
	return AgentCommand{
		CommandID:       row.CommandID,
		AgentID:         row.AgentID,
		QueueGeneration: row.QueueGeneration,
		CommandType:     row.CommandType,
		Payload:         CloneJSON(row.Payload),
		Status:          row.Status,
		AttemptCount:    row.AttemptCount,
		NextRetryAt:     row.NextRetryAt,
		AckedAt:         row.AckedAt,
		LastError:       CloneJSON(row.LastError),
		Result:          CloneJSON(row.Result),
		CreatedAt:       row.CreatedAt,
		DispatchedAt:    row.DispatchedAt,
	}
}

func agentCommandToORM(command AgentCommand) ormAgentCommand {
	return ormAgentCommand{
		CommandID:       command.CommandID,
		AgentID:         command.AgentID,
		QueueGeneration: command.QueueGeneration,
		CommandType:     command.CommandType,
		Payload:         CloneJSON(command.Payload),
		Status:          command.Status,
		AttemptCount:    command.AttemptCount,
		NextRetryAt:     command.NextRetryAt,
		AckedAt:         command.AckedAt,
		LastError:       CloneJSON(command.LastError),
		Result:          CloneJSON(command.Result),
		CreatedAt:       command.CreatedAt,
		DispatchedAt:    command.DispatchedAt,
	}
}
