package control

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"go.uber.org/zap"

	internal "github.com/lazymind/file_watcher/internal"
	"github.com/lazymind/file_watcher/internal/config"
)

// CommandDispatcher receives control-plane commands and dispatches them.
type CommandDispatcher interface {
	HandleCommand(ctx context.Context, cmd internal.Command) (any, error)
}

// HeartbeatReporter periodically reports heartbeats and pulls control-plane commands.
type HeartbeatReporter struct {
	cfg        *config.Config
	client     ControlPlaneClient
	dispatcher CommandDispatcher
	statusFn   func() internal.AgentStatus                     // current Agent status
	statsFn    func() (sourceCount, watchCount, taskCount int) // runtime statistics
	log        *zap.Logger
}

func NewHeartbeatReporter(
	cfg *config.Config,
	client ControlPlaneClient,
	dispatcher CommandDispatcher,
	statusFn func() internal.AgentStatus,
	statsFn func() (int, int, int),
	log *zap.Logger,
) *HeartbeatReporter {
	return &HeartbeatReporter{
		cfg:        cfg,
		client:     client,
		dispatcher: dispatcher,
		statusFn:   statusFn,
		statsFn:    statsFn,
		log:        log,
	}
}

// Run starts the heartbeat and command-pull goroutines, then blocks until ctx is canceled.
func (h *HeartbeatReporter) Run(ctx context.Context) {
	go h.heartbeatLoop(ctx)
	go h.pullLoop(ctx)
	<-ctx.Done()
}

func (h *HeartbeatReporter) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(h.cfg.HeartbeatInterval)
	defer ticker.Stop()

	hostname, _ := os.Hostname()
	advertiseAddr := h.cfg.AgentListenURL()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sc, wc, tc := h.statsFn()
			payload := internal.HeartbeatPayload{
				AgentID:          h.cfg.AgentID,
				TenantID:         h.cfg.TenantID,
				Hostname:         hostname,
				Version:          "0.1.0",
				Status:           h.statusFn(),
				LastHeartbeatAt:  time.Now(),
				SourceCount:      sc,
				ActiveWatchCount: wc,
				ActiveTaskCount:  tc,
				ListenAddr:       advertiseAddr,
			}
			if err := h.client.ReportHeartbeat(ctx, payload); err != nil {
				h.log.Warn("heartbeat failed", zap.Error(err))
			} else {
				h.log.Debug("heartbeat reported",
					zap.String("agent_id", h.cfg.AgentID),
					zap.Int("source_count", sc),
					zap.Int("watch_count", wc),
					zap.Int("task_count", tc),
				)
			}
		}
	}
}

func (h *HeartbeatReporter) pullLoop(ctx context.Context) {
	ticker := time.NewTicker(h.cfg.PullInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			resp, err := h.client.PullCommands(ctx, internal.PullCommandsRequest{
				AgentID:  h.cfg.AgentID,
				TenantID: h.cfg.TenantID,
			})
			if err != nil {
				h.log.Warn("pull commands failed", zap.Error(err))
				continue
			}
			if len(resp.Commands) > 0 {
				h.log.Info("pulled commands", zap.Int("count", len(resp.Commands)))
			}
			for _, cmd := range resp.Commands {
				h.log.Info("handling command",
					zap.Int64("command_id", cmd.ID),
					zap.String("exact_command_id", cmd.CommandID),
					zap.String("type", string(cmd.Type)),
					zap.String("source_id", cmd.SourceID),
					zap.String("document_id", cmd.DocumentID),
					zap.String("version_id", cmd.VersionID),
				)
				result, err := h.dispatcher.HandleCommand(ctx, cmd)
				ack := internal.AckCommandRequest{
					AgentID:         h.cfg.AgentID,
					CommandID:       cmd.CommandID,
					LegacyCommandID: cmd.ID,
					Success:         err == nil,
				}
				if err != nil {
					ack.Error = err.Error()
					h.log.Error("handle command failed", zap.String("type", string(cmd.Type)), zap.Int64("command_id", cmd.ID), zap.Error(err))
				} else if result != nil {
					if raw, marshalErr := json.Marshal(result); marshalErr == nil {
						ack.ResultJSON = string(raw)
					}
				}
				if cmd.CommandID == "" && cmd.ID <= 0 {
					continue
				}
				if ackErr := h.client.AckCommand(ctx, ack); ackErr != nil {
					h.log.Warn("ack command failed", zap.Int64("command_id", cmd.ID), zap.Error(ackErr))
				} else {
					h.log.Info("ack command sent",
						zap.Int64("command_id", cmd.ID),
						zap.Bool("success", ack.Success),
						zap.String("error", ack.Error),
					)
				}
			}
		}
	}
}
