package assistantbridge

import (
	"context"
	"time"

	"lazymind/agentconnector/internal/agentintegration"
)

const agentConnectTimeout = 5 * time.Minute

type agentConnection struct {
	status agentintegration.Status
	cancel context.CancelFunc
	done   chan struct{}
}

// The installation belongs to the local service, not the lifetime of a browser
// request. Status polling and repeated clicks observe the same operation.
func (s *Server) startAgentConnection(ctx context.Context, agent string) (agentintegration.Status, error) {
	s.connectionMu.Lock()
	defer s.connectionMu.Unlock()
	if current := s.connections[agent]; current != nil && current.status.State == agentintegration.Connecting {
		return current.status, nil
	}
	status, err := s.agentStatus(ctx, agent)
	if err != nil || status.State != agentintegration.Ready {
		return status, err
	}
	status.State = agentintegration.Connecting
	status.Message = "Installing the LazyMind plugin into your existing DSH. You can leave this page and return to check progress."
	jobCtx, cancel := context.WithTimeout(context.Background(), agentConnectTimeout)
	job := &agentConnection{status: status, cancel: cancel, done: make(chan struct{})}
	s.connections[agent] = job
	go func() {
		defer cancel()
		var result agentintegration.Status
		if s.connectOverride != nil {
			result = s.connectOverride(jobCtx, agent)
		} else {
			var err error
			result, err = s.agentAction(jobCtx, agent, "connect")
			if err != nil {
				result = agentintegration.Fail(status, err.Error())
			}
		}
		s.connectionMu.Lock()
		defer s.connectionMu.Unlock()
		job.status = result
		if result.State == agentintegration.Enabled {
			delete(s.connections, agent)
		}
		close(job.done)
	}()
	return status, nil
}

func (s *Server) connectionStatus(status agentintegration.Status) agentintegration.Status {
	s.connectionMu.Lock()
	defer s.connectionMu.Unlock()
	if job := s.connections[status.Agent]; job != nil {
		result := job.status
		result.Requirements = status.Requirements
		result.ExecutablePath = status.ExecutablePath
		return result
	}
	return status
}

func (s *Server) cancelAgentConnections() {
	s.connectionMu.Lock()
	defer s.connectionMu.Unlock()
	for _, job := range s.connections {
		job.cancel()
	}
}
