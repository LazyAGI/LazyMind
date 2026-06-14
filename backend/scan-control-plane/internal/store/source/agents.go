package source

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

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

func (r *SQLRepository) CreateAgentCommand(ctx context.Context, command AgentCommand) error {
	db := r.ormDB(ctx)
	if db == nil {
		return NewStoreError(ErrCodeInternal, "orm repository is not initialized")
	}
	if command.Status == "" {
		command.Status = "PENDING"
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
	return mapSQLConstraint(db.Create(agentCommandToORM(command)).Error)
}

func (r *SQLRepository) ListPendingAgentCommands(ctx context.Context, agentID string, now time.Time, limit int) ([]AgentCommand, error) {
	if limit <= 0 {
		limit = 50
	}
	var commands []AgentCommand
	err := r.withORMTx(ctx, func(tx *gorm.DB) error {
		var rows []ormAgentCommand
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("agent_id = ? AND status = ?", agentID, "PENDING").
			Where("next_retry_at IS NULL OR next_retry_at <= ?", now).
			Order("created_at, command_id").
			Limit(limit).
			Find(&rows).Error; err != nil {
			return err
		}
		commands = make([]AgentCommand, 0, len(rows))
		for _, row := range rows {
			command := agentCommandFromORM(row)
			commands = append(commands, command)
			row.Status = "DISPATCHED"
			row.AttemptCount++
			row.DispatchedAt = &now
			if err := tx.Model(&ormAgentCommand{}).
				Where("command_id = ? AND status = ?", row.CommandID, "PENDING").
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
		CommandID:    row.CommandID,
		AgentID:      row.AgentID,
		CommandType:  row.CommandType,
		Payload:      CloneJSON(row.Payload),
		Status:       row.Status,
		AttemptCount: row.AttemptCount,
		NextRetryAt:  row.NextRetryAt,
		AckedAt:      row.AckedAt,
		LastError:    CloneJSON(row.LastError),
		Result:       CloneJSON(row.Result),
		CreatedAt:    row.CreatedAt,
		DispatchedAt: row.DispatchedAt,
	}
}

func agentCommandToORM(command AgentCommand) ormAgentCommand {
	return ormAgentCommand{
		CommandID:    command.CommandID,
		AgentID:      command.AgentID,
		CommandType:  command.CommandType,
		Payload:      CloneJSON(command.Payload),
		Status:       command.Status,
		AttemptCount: command.AttemptCount,
		NextRetryAt:  command.NextRetryAt,
		AckedAt:      command.AckedAt,
		LastError:    CloneJSON(command.LastError),
		Result:       CloneJSON(command.Result),
		CreatedAt:    command.CreatedAt,
		DispatchedAt: command.DispatchedAt,
	}
}
