package paperguide

import (
	"bytes"
	"encoding/json"
)

type InteractionKind string

const (
	InteractionStepper    InteractionKind = "stepper"
	InteractionComparison InteractionKind = "comparison"
	InteractionResults    InteractionKind = "results"
	InteractionCalculator InteractionKind = "calculator"
)

type InteractionProvenance string

const (
	InteractionReported     InteractionProvenance = "reported"
	InteractionDerived      InteractionProvenance = "derived"
	InteractionIllustrative InteractionProvenance = "illustrative"
)

type GuideInteraction struct {
	Kind           InteractionKind             `json:"kind"`
	Title          string                      `json:"title"`
	Provenance     InteractionProvenance       `json:"provenance"`
	Explanation    string                      `json:"explanation"`
	Steps          []GuideInteractionStep      `json:"steps"`
	Rows           []GuideInteractionRow       `json:"rows"`
	Metric         string                      `json:"metric"`
	Unit           string                      `json:"unit"`
	HigherIsBetter bool                        `json:"higher_is_better"`
	Parameters     []GuideInteractionParameter `json:"parameters"`
	Outputs        []GuideInteractionOutput    `json:"outputs"`
}

type GuideInteractionStep struct {
	Label string `json:"label"`
	Text  string `json:"text"`
}

type GuideInteractionRow struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Group string  `json:"group"`
	Note  string  `json:"note"`
}

type GuideInteractionParameter struct {
	ID    string  `json:"id"`
	Label string  `json:"label"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Step  float64 `json:"step"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

type GuideInteractionOutput struct {
	Label  string   `json:"label"`
	Unit   string   `json:"unit"`
	Tokens []string `json:"tokens"`
}

// Unlike older guide blocks, interaction wire fields are all required,
// including false, empty strings, and empty arrays. JSON null is not a value.
func decodeInteractionObject[T any](data []byte, target *T, fields ...string) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw) != len(fields) {
		return invalid("unexpected or missing interaction fields")
	}
	for _, field := range fields {
		value := raw[field]
		if value == nil || bytes.Equal(value, []byte("null")) {
			return invalid("missing or null interaction field")
		}
	}
	return json.Unmarshal(data, target)
}

func (i *GuideInteraction) UnmarshalJSON(data []byte) error {
	type wire GuideInteraction
	return decodeInteractionObject(data, (*wire)(i), "kind", "title", "provenance", "explanation",
		"steps", "rows", "metric", "unit", "higher_is_better", "parameters", "outputs")
}

func (s *GuideInteractionStep) UnmarshalJSON(data []byte) error {
	type wire GuideInteractionStep
	return decodeInteractionObject(data, (*wire)(s), "label", "text")
}

func (r *GuideInteractionRow) UnmarshalJSON(data []byte) error {
	type wire GuideInteractionRow
	return decodeInteractionObject(data, (*wire)(r), "label", "value", "group", "note")
}

func (p *GuideInteractionParameter) UnmarshalJSON(data []byte) error {
	type wire GuideInteractionParameter
	return decodeInteractionObject(data, (*wire)(p), "id", "label", "min", "max", "step", "value", "unit")
}

func (o *GuideInteractionOutput) UnmarshalJSON(data []byte) error {
	type wire GuideInteractionOutput
	return decodeInteractionObject(data, (*wire)(o), "label", "unit", "tokens")
}
