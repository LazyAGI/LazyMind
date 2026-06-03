package source

import (
	"database/sql/driver"
	"encoding/json"
)

type JSON map[string]any

func (j JSON) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	body, err := json.Marshal(j)
	if err != nil {
		return nil, err
	}
	return string(body), nil
}

func (j *JSON) Scan(value any) error {
	if value == nil {
		*j = nil
		return nil
	}
	var body []byte
	switch typed := value.(type) {
	case []byte:
		body = typed
	case string:
		body = []byte(typed)
	default:
		*j = nil
		return nil
	}
	*j = decodeJSON(body)
	return nil
}

func CloneJSON(in JSON) JSON {
	if in == nil {
		return nil
	}
	out := make(JSON, len(in))
	for key, value := range in {
		out[key] = cloneJSONValue(value)
	}
	return out
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case JSON:
		return CloneJSON(typed)
	case map[string]any:
		return CloneJSON(JSON(typed))
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneJSONValue(item)
		}
		return out
	case []string:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = item
		}
		return out
	case json.Number:
		return typed
	default:
		return value
	}
}
