package contract

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

func EncodeOffsetPageToken(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("offset:%d", offset)))
}

func DecodeOffsetPageToken(token string) (int, error) {
	if token == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, err
	}
	value := string(raw)
	if !strings.HasPrefix(value, "offset:") {
		return 0, fmt.Errorf("unsupported token")
	}
	offset, err := strconv.Atoi(strings.TrimPrefix(value, "offset:"))
	if err != nil {
		return 0, err
	}
	if offset < 0 {
		return 0, fmt.Errorf("negative offset")
	}
	return offset, nil
}
