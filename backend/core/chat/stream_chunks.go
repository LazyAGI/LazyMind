package chat

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

const maxStreamMessageBytes = 32 * 1024 * 1024
const streamChunkBytes = 180 * 1024
const maxStreamChunkLineBytes = 256 * 1024

// Chunks are contiguous and ordered on the HTTP stream. Allow only one pending
// message so interleaving cannot reorder business events or grow memory unbounded.
type streamChunkDecoder struct {
	id                string
	total, next, size int
	buffer            []byte
}

type streamChunk struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Index   *int   `json:"index"`
	Total   int    `json:"total"`
	Size    int    `json:"size"`
	Payload string `json:"payload"`
}

func (d *streamChunkDecoder) accept(line []byte) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(line, &envelope); err != nil {
		return nil, fmt.Errorf("invalid algorithm stream frame: %w", err)
	}
	raw, chunked := envelope["stream_chunk"]
	if !chunked {
		if d.id != "" {
			return nil, fmt.Errorf("algorithm stream interrupted chunked message")
		}
		return line, nil
	}
	var c streamChunk
	if len(line) > maxStreamChunkLineBytes || len(envelope) != 1 {
		return nil, fmt.Errorf("invalid algorithm chunk envelope")
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("invalid algorithm chunk: %w", err)
	}
	if c.Version != 1 || len(c.ID) == 0 || len(c.ID) > 64 || c.Index == nil || c.Size <= 0 || c.Size > maxStreamMessageBytes || c.Total != (c.Size+streamChunkBytes-1)/streamChunkBytes || *c.Index < 0 || *c.Index >= c.Total {
		return nil, fmt.Errorf("invalid algorithm chunk metadata")
	}
	if d.id == "" {
		if *c.Index != 0 {
			return nil, fmt.Errorf("algorithm chunk sequence must start at zero")
		}
		d.id, d.total, d.size = c.ID, c.Total, c.Size
	}
	if c.ID != d.id || c.Total != d.total || c.Size != d.size || *c.Index != d.next {
		return nil, fmt.Errorf("algorithm chunk sequence mismatch")
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(c.Payload)
	expected := streamChunkBytes
	if d.next == d.total-1 {
		expected = d.size - d.next*streamChunkBytes
	}
	if err != nil || len(decoded) != expected {
		return nil, fmt.Errorf("invalid algorithm chunk payload")
	}
	d.buffer = append(d.buffer, decoded...)
	d.next++
	if d.next != d.total {
		return nil, nil
	}
	result := d.buffer
	*d = streamChunkDecoder{}
	if !utf8.Valid(result) {
		return nil, fmt.Errorf("invalid UTF-8 in algorithm chunked message")
	}
	var complete map[string]json.RawMessage
	if err := json.Unmarshal(result, &complete); err != nil {
		return nil, fmt.Errorf("invalid reassembled algorithm frame: %w", err)
	}
	if _, nested := complete["stream_chunk"]; nested {
		return nil, fmt.Errorf("nested algorithm chunks are not supported")
	}
	return result, nil
}
