package capability

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
)

const cursorVersion = 1

type pageCursor struct {
	Version     int    `json:"v"`
	Kind        string `json:"k"`
	Fingerprint string `json:"f"`
	Offset      int    `json:"o"`
}

func encodePageToken(kind, fingerprint string, offset int) (string, error) {
	payload, err := json.Marshal(pageCursor{Version: cursorVersion, Kind: kind, Fingerprint: fingerprint, Offset: offset})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodePageToken(token, kind, fingerprint string) (int, error) {
	if token == "" {
		return 0, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, err
	}
	var cursor pageCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return 0, err
	}
	if cursor.Version != cursorVersion || cursor.Kind != kind || cursor.Fingerprint != fingerprint || cursor.Offset < 0 {
		return 0, errInvalidCursor
	}
	return cursor.Offset, nil
}

func pageFingerprint(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

var errInvalidCursor = NewError(InvalidArgument, "pagination", "invalid page token", false, nil)
