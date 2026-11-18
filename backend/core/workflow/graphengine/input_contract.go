package graphengine

import (
	"encoding/json"

	"github.com/google/jsonschema-go/jsonschema"
)

// InputContract describes a package-owned external material to every host.
// Schemas validate JSON file contents before a workflow session is created.
type InputContract struct {
	Description string         `json:"description,omitempty" yaml:"description,omitempty"`
	Schema      map[string]any `json:"schema,omitempty" yaml:"schema,omitempty"`
}

func (c InputContract) ResolveSchema() (*jsonschema.Resolved, error) {
	if c.Schema == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(c.Schema)
	if err != nil {
		return nil, err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(encoded, &schema); err != nil {
		return nil, err
	}
	return schema.Resolve(nil)
}
