package chat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- parsePluginEventFromSSELine ----

func TestParsePluginEventFromSSELine_StepTrigger(t *testing.T) {
	payload := PluginEvent{
		Type:            "step_trigger",
		PluginSessionID: "sess-1",
		PluginID:        "image-plugin",
		StepID:          "optimize_prompt",
		StepMode:        "auto",
		UserInput:       "a cute cat",
	}
	b, _ := json.Marshal(payload)
	line := "data: " + string(b)
	ev := parsePluginEventFromSSELine(line)
	require.NotNil(t, ev)
	assert.Equal(t, "step_trigger", ev.Type)
	assert.Equal(t, "sess-1", ev.PluginSessionID)
	assert.Equal(t, "optimize_prompt", ev.StepID)
	assert.Equal(t, "auto", ev.StepMode)
}

// TestParsePluginEventFromSSELine_PythonWrappedFormat covers the Python ChatAgent wire format:
// {"type": "plugin_event", "data": {<actual event>}}
func TestParsePluginEventFromSSELine_PythonWrappedFormat(t *testing.T) {
	inner := map[string]interface{}{
		"type":              "step_trigger",
		"plugin_session_id": "sess-2",
		"plugin_id":         "image-plugin",
		"step_id":           "generate_image",
		"step_mode":         "auto",
		"user_input":        "a fluffy cat",
	}
	wrapped := map[string]interface{}{
		"type": "plugin_event",
		"data": inner,
	}
	b, _ := json.Marshal(wrapped)
	line := "data: " + string(b)
	ev := parsePluginEventFromSSELine(line)
	require.NotNil(t, ev)
	assert.Equal(t, "step_trigger", ev.Type)
	assert.Equal(t, "sess-2", ev.PluginSessionID)
	assert.Equal(t, "generate_image", ev.StepID)
}

func TestParsePluginEventFromSSELine_Artifact(t *testing.T) {
	payload := PluginEvent{
		Type:       "artifact",
		ArtifactID: "optimized_prompt",
		Value:      "a fluffy orange cat in watercolor style",
	}
	b, _ := json.Marshal(payload)
	line := "data: " + string(b)
	ev := parsePluginEventFromSSELine(line)
	require.NotNil(t, ev)
	assert.Equal(t, "artifact", ev.Type)
	assert.Equal(t, "optimized_prompt", ev.ArtifactID)
}

func TestParsePluginEventFromSSELine_ReturnNilForNonEvent(t *testing.T) {
	ev := parsePluginEventFromSSELine("data: [DONE]")
	assert.Nil(t, ev)
}

func TestParsePluginEventFromSSELine_ReturnNilForMalformed(t *testing.T) {
	ev := parsePluginEventFromSSELine("data: {bad json")
	assert.Nil(t, ev)
}

func TestParsePluginEventFromSSELine_ReturnNilForEmptyType(t *testing.T) {
	ev := parsePluginEventFromSSELine(`data: {"no_type": "here"}`)
	assert.Nil(t, ev)
}

// ---- buildStepWaitingEvent / buildStepChangeEvent ----

func TestBuildStepWaitingEvent(t *testing.T) {
	ev := buildStepWaitingEvent("session-1", "generate_image")
	assert.Equal(t, "step_waiting", ev["type"])
	assert.Equal(t, "session-1", ev["plugin_session_id"])
	assert.Equal(t, "generate_image", ev["step_id"])
}

func TestBuildStepChangeEvent(t *testing.T) {
	ev := buildStepChangeEvent("session-2", "optimize_prompt")
	assert.Equal(t, "step_change", ev["type"])
	assert.Equal(t, "session-2", ev["plugin_session_id"])
	assert.Equal(t, "optimize_prompt", ev["step_id"])
}

// ---- InjectDriverJudgment ----

func TestInjectDriverJudgment_AppendsUserMessage(t *testing.T) {
	history := []map[string]string{
		{"role": "user", "content": "hello"},
		{"role": "assistant", "content": "hi"},
	}
	updated := InjectDriverJudgment(history, "PASS — proceed to next step.")
	require.Len(t, updated, 3)
	assert.Equal(t, "user", updated[2]["role"])
	assert.Equal(t, "PASS — proceed to next step.", updated[2]["content"])
}

func TestInjectDriverJudgment_EmptyHistory(t *testing.T) {
	updated := InjectDriverJudgment(nil, "judgment")
	require.Len(t, updated, 1)
	assert.Equal(t, "judgment", updated[0]["content"])
}

// ---- newPluginID ----

func TestNewPluginID_IsUnique(t *testing.T) {
	id1 := newPluginID()
	id2 := newPluginID()
	assert.NotEmpty(t, id1)
	assert.NotEmpty(t, id2)
	assert.NotEqual(t, id1, id2)
}

func TestNewPluginID_HasPrefix(t *testing.T) {
	id := newPluginID()
	assert.True(t, len(id) > 3, "plugin ID should be non-trivially long")
}

// ---- checkStepDependencies (no DB) ----

func TestCheckStepDependencies_NilDB_NoError(t *testing.T) {
	trigger := &StepTriggerInfo{
		PluginSessionID: "sess",
		PluginID:        "test",
		StepID:          "step_b",
		Inputs: []StepInputSpec{
			{ArtifactID: "step_a_output", Required: true},
		},
	}
	err := checkStepDependencies(nil, trigger)
	assert.NoError(t, err)
}

func TestCheckStepDependencies_EmptyInputs_NoError(t *testing.T) {
	trigger := &StepTriggerInfo{
		PluginSessionID: "sess",
		PluginID:        "test",
		StepID:          "step_b",
		Inputs:          []StepInputSpec{},
	}
	err := checkStepDependencies(nil, trigger)
	assert.NoError(t, err)
}
