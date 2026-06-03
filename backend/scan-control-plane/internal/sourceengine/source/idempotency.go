package source

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func createRequestHash(req CreateSourceRequest) (string, error) {
	type hashable struct {
		Name              string         `json:"name"`
		Bindings          []BindingInput `json:"bindings"`
		IncludeExtensions []string       `json:"include_extensions,omitempty"`
		ExcludeExtensions []string       `json:"exclude_extensions,omitempty"`
		SourceOptions     map[string]any `json:"source_options,omitempty"`
	}
	body, err := json.Marshal(hashable{
		Name:              req.Name,
		Bindings:          req.Bindings,
		IncludeExtensions: req.IncludeExtensions,
		ExcludeExtensions: req.ExcludeExtensions,
		SourceOptions:     req.SourceOptions,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}
