package chat

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// parseListPageToken matches wordgroup/doc list pagination (offset into ordered list).
func parseListPageToken(token string) (int, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, nil
	}
	if v, err := strconv.Atoi(token); err == nil && v >= 0 {
		return v, nil
	}
	decoders := []*base64.Encoding{
		base64.RawStdEncoding,
		base64.StdEncoding,
		base64.RawURLEncoding,
		base64.URLEncoding,
	}
	for _, decoder := range decoders {
		b, err := decoder.DecodeString(token)
		if err != nil {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(b, &payload); err != nil {
			continue
		}
		for _, key := range []string{"Start", "start", "offset", "Offset"} {
			if raw, ok := payload[key]; ok {
				switch v := raw.(type) {
				case float64:
					if v >= 0 {
						return int(v), nil
					}
				case int:
					if v >= 0 {
						return v, nil
					}
				case string:
					if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
						return n, nil
					}
				}
			}
		}
	}
	return 0, errors.New("invalid page_token")
}

func encodeListPageToken(start, limit, total int) string {
	payload := map[string]int{
		"Start":      start,
		"Limit":      limit,
		"TotalCount": total,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return strconv.Itoa(start)
	}
	return base64.RawStdEncoding.EncodeToString(b)
}
