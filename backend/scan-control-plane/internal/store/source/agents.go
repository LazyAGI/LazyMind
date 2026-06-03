package source

import (
	"context"
	"errors"

	"gorm.io/gorm"
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
