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

type AgentCommand struct {
	CommandID     string
	AgentID       string
	CommandType   string
	Payload       JSON
	Status        string
	AttemptCount  int64
	NextRetryAt   *time.Time
	AckedAt       *time.Time
	LastError     JSON
	Result        JSON
	CreatedAt     time.Time
	DispatchedAt  *time.Time
}

type AgentCommandAck struct {
	AgentID   string
	CommandID string
	Success   bool
	Error     string
	Result    JSON
	AckedAt   time.Time
}
