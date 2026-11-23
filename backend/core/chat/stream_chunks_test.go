package chat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func chunkLines(t *testing.T, raw []byte) []string {
	t.Helper()
	total := (len(raw) + streamChunkBytes - 1) / streamChunkBytes
	var lines []string
	for start, index := 0, 0; start < len(raw); start, index = start+streamChunkBytes, index+1 {
		end := start + streamChunkBytes
		if end > len(raw) {
			end = len(raw)
		}
		b, err := json.Marshal(map[string]any{"stream_chunk": map[string]any{"version": 1, "id": "test", "index": index, "total": total, "size": len(raw), "payload": base64.StdEncoding.EncodeToString(raw[start:end])}})
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	return lines
}

func TestChunkedAlgorithmStreamRoundTrip(t *testing.T) {
	for _, key := range []string{"text", "canonical_messages"} {
		t.Run(key, func(t *testing.T) {
			text := strings.Repeat("中文😀\"\\\n", 90000)
			value := any(text)
			if key == "canonical_messages" {
				value = []map[string]any{{"role": "tool", "content": text}}
			}
			raw := algorithmFrame(t, map[string]any{key: value})
			lines := chunkLines(t, []byte(raw))
			for _, line := range lines {
				if len(line) > maxStreamChunkLineBytes {
					t.Fatal("oversized line")
				}
			}
			wire := algorithmFrame(t, map[string]any{"text": "before"}) + "\n" + strings.Join(lines, "\n\n") + "\n" + algorithmFrame(t, map[string]any{"text": "after"}) + "\n"
			count := 0
			for data := range lazyStreamHandler(context.Background(), &http.Response{Body: io.NopCloser(strings.NewReader(wire))}) {
				if data.Err != nil {
					t.Fatal(data.Err)
				}
				if count == 1 {
					got, _ := json.Marshal(data.Resp.Data)
					var want LazyChatResponse
					if err := json.Unmarshal([]byte(raw), &want); err != nil {
						t.Fatal(err)
					}
					expected, _ := json.Marshal(want.Data)
					if string(got) != string(expected) {
						t.Fatal("reassembled content differs")
					}
				}
				count++
			}
			if count != 3 {
				t.Fatalf("got %d business frames", count)
			}
		})
	}
}

func TestChunkedAlgorithmStreamRejectsIncompleteAndInvalid(t *testing.T) {
	lines := chunkLines(t, []byte(algorithmFrame(t, map[string]any{"text": strings.Repeat("x", 700000)})))
	cases := map[string]string{
		"missing": lines[0], "duplicate": lines[0] + "\n" + lines[0],
		"out_of_order": lines[1], "interleaved": lines[0] + "\n" + `{"code":200}`,
		"oversized":      `{"stream_chunk":{"version":1,"id":"a","index":0,"total":999,"size":33554433,"payload":"AA=="}}`,
		"invalid_base64": strings.Replace(lines[0], `"payload":"`, `"payload":"!`, 1),
		"version":        strings.Replace(lines[0], `"version":1`, `"version":2`, 1),
	}
	for name, wire := range cases {
		t.Run(name, func(t *testing.T) {
			count := 0
			for data := range lazyStreamHandler(context.Background(), &http.Response{Body: io.NopCloser(strings.NewReader(wire + "\n"))}) {
				if data.Err == nil || data.ErrKind != lazyStreamErrorProtocol {
					t.Fatalf("expected protocol error: %+v", data)
				}
				count++
			}
			if count != 1 {
				t.Fatalf("got %d events", count)
			}
		})
	}
}

func TestChunkedAlgorithmStreamCancellationClosesBody(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := lazyStreamHandler(ctx, &http.Response{Body: reader})
	cancel()
	select {
	case _, ok := <-output:
		if ok {
			t.Fatal("unexpected event")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not unblock reader")
	}
}
