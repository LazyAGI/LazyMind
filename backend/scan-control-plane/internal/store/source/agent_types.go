package source

import "time"

type Agent struct {
	AgentID           string
	TenantID          string
	Hostname          string
	Version           string
	Status            string
	ListenAddr        string
	LastHeartbeatAt   time.Time
	ActiveSourceCount int64
	ActiveWatchCount  int64
	ActiveTaskCount   int64
	UpdatedAt         time.Time
}
