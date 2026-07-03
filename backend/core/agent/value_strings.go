package agent

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

func agentScalarString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case json.Number:
		return typed.String()
	case bool:
		return strconv.FormatBool(typed)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case int8, int16, int32, int64:
		return fmt.Sprintf("%d", typed)
	case uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", typed)
	default:
		if bytesValue, ok := value.([]byte); ok {
			return string(bytesValue)
		}
		if isJSONLikeValue(value) {
			if encoded, err := json.Marshal(value); err == nil {
				return string(encoded)
			}
		}
		return fmt.Sprint(value)
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func isJSONLikeValue(value any) bool {
	if value == nil {
		return false
	}
	kind := reflect.TypeOf(value).Kind()
	return kind == reflect.Map || kind == reflect.Slice || kind == reflect.Array || kind == reflect.Struct
}
