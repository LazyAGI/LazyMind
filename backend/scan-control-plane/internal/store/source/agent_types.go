package source

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"time"
)

const AgentCommandQueueGeneration int64 = 2

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
	CommandID       string
	AgentID         string
	QueueGeneration int64
	CommandType     string
	Payload         JSON
	Status          string
	AttemptCount    int64
	NextRetryAt     *time.Time
	AckedAt         *time.Time
	LastError       JSON
	Result          JSON
	CreatedAt       time.Time
	DispatchedAt    *time.Time
}

func WatcherCommandID(agentID string, binding Binding, commandType string) string {
	seed := fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%d\x00%s", AgentCommandQueueGeneration, agentID, binding.SourceID, binding.BindingID, binding.BindingGeneration, commandType)
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(seed))
	value := hash.Sum64() & ((uint64(1) << 63) - 1)
	if value == 0 {
		value = 1
	}
	return strconv.FormatUint(value, 10)
}

func WatcherRecoveryRequestID(binding Binding, now time.Time) string {
	return fmt.Sprintf("watcher-recovery-v%d-%s-%d-%s", AgentCommandQueueGeneration, binding.BindingID, binding.BindingGeneration, now.UTC().Truncate(time.Minute).Format("200601021504"))
}

type AgentCommandAck struct {
	AgentID   string
	CommandID string
	Success   bool
	Error     string
	Result    JSON
	AckedAt   time.Time
}
