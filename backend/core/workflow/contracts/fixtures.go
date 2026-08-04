package contracts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
)

const VersionV1 = "workflow.v1"

type Attempt struct {
	AttemptID       string         `json:"attempt_id"`
	StepID          string         `json:"step_id"`
	Status          string         `json:"status"`
	Operation       string         `json:"operation"`
	AttemptNo       int            `json:"attempt_no"`
	PartialSelector []string       `json:"partial_selector,omitempty"`
	Context         AttemptContext `json:"context"`
}

// ValidateCapturedBaseline is the deterministic capture/replay harness used by
// the legacy Runtime package tests. root is the repository root.
func ValidateCapturedBaseline(root string) error {
	base := filepath.Join(root, "docs", "plan", "plugin", "contracts", "v1")
	manifest, err := ReadBaselineManifest(filepath.Join(base, "baseline-manifest.json"))
	if err != nil {
		return err
	}
	for _, source := range manifest.ProductionSources {
		if _, err := os.Stat(filepath.Join(root, source)); err != nil {
			return fmt.Errorf("production source %s: %w", source, err)
		}
	}
	seenSymbols := map[string]bool{}
	for _, scenario := range manifest.RequiredScenarios {
		binding, ok := manifest.ScenarioTests[scenario]
		if !ok {
			return fmt.Errorf("scenario %s has no production test binding", scenario)
		}
		key := binding.Source + ":" + binding.Symbol
		if seenSymbols[key] {
			return fmt.Errorf("production test %s is bound more than once", key)
		}
		seenSymbols[key] = true
		if err := requireSymbol(root, binding.Source, `func\s+`+regexp.QuoteMeta(binding.Symbol)+`\s*\(`); err != nil {
			return fmt.Errorf("scenario %s: %w", scenario, err)
		}
	}
	for tool, semantics := range manifest.ToolSemantics {
		if err := requireSymbol(root, semantics.ProductionSource, `def\s+`+regexp.QuoteMeta(semantics.ProductionSymbol)+`\s*\(`); err != nil {
			return fmt.Errorf("tool %s: %w", tool, err)
		}
		if err := requireSymbol(root, semantics.TransitionSource, `func\s+`+regexp.QuoteMeta(semantics.TransitionSymbol)+`\s*\(`); err != nil {
			return fmt.Errorf("tool %s transition: %w", tool, err)
		}
	}
	paths, err := filepath.Glob(filepath.Join(base, "golden", "*.json"))
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, path := range paths {
		fixture, err := ReadGolden(path)
		if err != nil {
			return fmt.Errorf("capture %s: %w", path, err)
		}
		seen[fixture.Scenario] = true
	}
	for _, scenario := range manifest.RequiredScenarios {
		if !seen[scenario] {
			return fmt.Errorf("missing captured scenario %s", scenario)
		}
	}
	return nil
}

func requireSymbol(root, source, pattern string) error {
	if source == "" {
		return fmt.Errorf("empty symbol source")
	}
	data, err := os.ReadFile(filepath.Join(root, source))
	if err != nil {
		return err
	}
	matched, err := regexp.Match(pattern, data)
	if err != nil {
		return err
	}
	if !matched {
		return fmt.Errorf("symbol matching %q not found in %s", pattern, source)
	}
	return nil
}

type InputBinding struct {
	ResourceID string `json:"resource_id"`
	RevisionID string `json:"revision_id"`
	BindAs     string `json:"bind_as"`
}
type AttemptContext struct {
	ContractVersion    string         `json:"contract_version"`
	SessionID          string         `json:"session_id"`
	StepID             string         `json:"step_id"`
	AttemptID          string         `json:"attempt_id"`
	AttemptNo          int            `json:"attempt_no"`
	Operation          string         `json:"operation"`
	RuntimeInstruction string         `json:"runtime_instruction,omitempty"`
	PartialSelector    []string       `json:"partial_selector,omitempty"`
	InputBindings      []InputBinding `json:"input_bindings"`
}

type Artifact struct {
	ArtifactID        string `json:"artifact_id"`
	Slot              string `json:"slot"`
	Revision          int    `json:"revision"`
	ProducerAttemptID string `json:"producer_attempt_id"`
	Stale             bool   `json:"stale"`
}

type Event struct {
	Cursor       int            `json:"cursor"`
	Type         string         `json:"type"`
	EntityID     string         `json:"entity_id"`
	StateVersion int            `json:"state_version"`
	Payload      map[string]any `json:"payload"`
}

// ReplayProjection applies only durable event payloads. It deliberately has no
// access to GoldenScenario.Projection, so tests prove the stream is sufficient.
func ReplayProjection(events []Event) (Projection, error) {
	var projection Projection
	for _, event := range events {
		raw, ok := event.Payload["projection"]
		if !ok {
			continue
		}
		data, err := json.Marshal(raw)
		if err != nil {
			return Projection{}, err
		}
		if err := json.Unmarshal(data, &projection); err != nil {
			return Projection{}, err
		}
	}
	if projection.SessionID == "" {
		return Projection{}, fmt.Errorf("event stream contains no projection")
	}
	return projection, nil
}

type ToolSemantics struct {
	Transition       string `json:"transition"`
	Wait             string `json:"wait"`
	ProductionSource string `json:"production_source"`
	ProductionSymbol string `json:"production_symbol"`
	TransitionSource string `json:"transition_source"`
	TransitionSymbol string `json:"transition_symbol"`
}

type TestBinding struct {
	Source string `json:"source"`
	Symbol string `json:"symbol"`
}

type ReplayRules struct {
	Ordering                          string `json:"ordering"`
	InitialEvent                      string `json:"initial_event"`
	TerminalProjectionIsAuthoritative bool   `json:"terminal_projection_is_authoritative"`
	EntityEventsReferenceEntities     bool   `json:"entity_events_must_reference_fixture_entities"`
}

type BaselineManifest struct {
	ContractVersion   string                   `json:"contract_version"`
	Authority         string                   `json:"authority"`
	ProductionSources []string                 `json:"production_sources"`
	RequiredScenarios []string                 `json:"required_scenarios"`
	ScenarioTests     map[string]TestBinding   `json:"scenario_tests"`
	ToolSemantics     map[string]ToolSemantics `json:"tool_semantics"`
	Replay            ReplayRules              `json:"replay"`
}

type Projection struct {
	SessionID    string   `json:"session_id"`
	Status       string   `json:"status"`
	ReadySteps   []string `json:"ready_steps"`
	StateVersion int      `json:"state_version"`
}

type GoldenScenario struct {
	ContractVersion string         `json:"contract_version"`
	Scenario        string         `json:"scenario"`
	Input           map[string]any `json:"input"`
	Projection      Projection     `json:"projection"`
	Attempts        []Attempt      `json:"attempts"`
	Artifacts       []Artifact     `json:"artifacts"`
	Events          []Event        `json:"events"`
}

func ReadGolden(path string) (GoldenScenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return GoldenScenario{}, err
	}
	var fixture GoldenScenario
	if err := json.Unmarshal(data, &fixture); err != nil {
		return GoldenScenario{}, err
	}
	if err := fixture.Validate(); err != nil {
		return GoldenScenario{}, err
	}
	return fixture, nil
}

func ReadBaselineManifest(path string) (BaselineManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BaselineManifest{}, err
	}
	var manifest BaselineManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return BaselineManifest{}, err
	}
	if manifest.ContractVersion != VersionV1 || manifest.Authority != "public_runtime" || len(manifest.ProductionSources) == 0 {
		return BaselineManifest{}, fmt.Errorf("invalid workflow baseline authority")
	}
	syncTool, syncOK := manifest.ToolSemantics["advance_step"]
	handoffTool, handoffOK := manifest.ToolSemantics["advance_step_and_hand_off"]
	if !syncOK || !handoffOK || syncTool.Transition == "" || syncTool.Transition != handoffTool.Transition || syncTool.Wait == handoffTool.Wait {
		return BaselineManifest{}, fmt.Errorf("advance tools must share transition and use different waits")
	}
	if manifest.Replay.Ordering != "cursor_strictly_increasing" || manifest.Replay.InitialEvent != "workflow.snapshot" {
		return BaselineManifest{}, fmt.Errorf("invalid workflow replay rules")
	}
	return manifest, nil
}

func (f GoldenScenario) Validate() error {
	if f.ContractVersion != VersionV1 || f.Scenario == "" || f.Projection.SessionID == "" {
		return fmt.Errorf("invalid workflow fixture identity")
	}
	if len(f.Events) == 0 || f.Events[0].Type != "workflow.snapshot" || f.Events[0].EntityID != f.Projection.SessionID {
		return fmt.Errorf("event sequence must start with the session snapshot")
	}
	previous := 0
	attemptIDs := make(map[string]struct{}, len(f.Attempts))
	attemptNos := make(map[string]int)
	for _, attempt := range f.Attempts {
		if attempt.AttemptID == "" || attempt.StepID == "" || attempt.AttemptNo < 1 || attempt.AttemptNo <= attemptNos[attempt.StepID] {
			return fmt.Errorf("invalid attempt fixture")
		}
		attemptIDs[attempt.AttemptID] = struct{}{}
		attemptNos[attempt.StepID] = attempt.AttemptNo
		context := attempt.Context
		if context.ContractVersion != VersionV1 || context.SessionID != f.Projection.SessionID || context.StepID != attempt.StepID || context.AttemptID != attempt.AttemptID || context.AttemptNo != attempt.AttemptNo || context.Operation != attempt.Operation || context.InputBindings == nil {
			return fmt.Errorf("attempt %s has invalid context", attempt.AttemptID)
		}
	}
	artifactRevisions := make(map[string]int)
	artifactIDs := make(map[string]struct{}, len(f.Artifacts))
	for _, artifact := range f.Artifacts {
		if artifact.ArtifactID == "" || artifact.Slot == "" || artifact.Revision < 1 {
			return fmt.Errorf("invalid artifact fixture")
		}
		if _, ok := attemptIDs[artifact.ProducerAttemptID]; !ok {
			return fmt.Errorf("artifact %s references unknown producer attempt", artifact.ArtifactID)
		}
		if artifact.Revision <= artifactRevisions[artifact.Slot] {
			return fmt.Errorf("artifact revisions for slot %s are not increasing", artifact.Slot)
		}
		artifactRevisions[artifact.Slot] = artifact.Revision
		artifactIDs[artifact.ArtifactID] = struct{}{}
	}
	for _, event := range f.Events {
		if event.Cursor <= previous || event.Type == "" || event.EntityID == "" || event.StateVersion < 1 || event.Payload == nil {
			return fmt.Errorf("invalid event sequence at cursor %d", event.Cursor)
		}
		if event.Type == "attempt.patch" {
			if _, ok := attemptIDs[event.EntityID]; !ok {
				return fmt.Errorf("event references unknown attempt %s", event.EntityID)
			}
		}
		if event.Type == "artifact.upsert" {
			if _, ok := artifactIDs[event.EntityID]; !ok {
				return fmt.Errorf("event references unknown artifact %s", event.EntityID)
			}
		}
		previous = event.Cursor
	}
	replayed, err := ReplayProjection(f.Events)
	if err != nil || !reflect.DeepEqual(replayed, f.Projection) {
		return fmt.Errorf("event replay does not reconstruct projection: %v", err)
	}
	return nil
}
