package source

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/lazymind/scan_control_plane/internal/coreclient"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

type DefaultEngine struct {
	repo               SourceRepository
	registry           connector.ConnectorRegistry
	core               coreclient.ResourceClient
	schedule           ScheduleEngine
	authStatus         AuthConnectionStatusClient
	clock              func() time.Time
	newID              func(string) string
	defaultDatasetAlgo coreclient.DatasetAlgo
}

type Option func(*DefaultEngine)

func NewDefaultEngine(repo SourceRepository, registry connector.ConnectorRegistry, core coreclient.ResourceClient, schedule ScheduleEngine, options ...Option) *DefaultEngine {
	if schedule == nil {
		panic("source schedule engine is required")
	}
	e := &DefaultEngine{
		repo:     repo,
		registry: registry,
		core:     core,
		schedule: schedule,
		clock:    time.Now,
		newID:    randomID,
	}
	for _, option := range options {
		option(e)
	}
	return e
}

func WithClock(clock func() time.Time) Option {
	return func(e *DefaultEngine) {
		if clock != nil {
			e.clock = clock
		}
	}
}

func WithIDGenerator(newID func(string) string) Option {
	return func(e *DefaultEngine) {
		if newID != nil {
			e.newID = newID
		}
	}
}

func WithDefaultDatasetAlgo(algo coreclient.DatasetAlgo) Option {
	return func(e *DefaultEngine) {
		e.defaultDatasetAlgo = algo
	}
}

func WithAuthConnectionStatusClient(client AuthConnectionStatusClient) Option {
	return func(e *DefaultEngine) {
		e.authStatus = client
	}
}

func randomID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return prefix + "-" + hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return prefix + "-" + hex.EncodeToString(b[:])
}
