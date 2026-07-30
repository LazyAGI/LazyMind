package doc

import "testing"

func TestMapParseTaskErrorFFmpegMissing(t *testing.T) {
	tests := []string{
		"ffmpeg not found in PATH.",
		"ffprobe not found in PATH.",
		"[Errno 2] No such file or directory: 'ffmpeg'",
	}

	for _, input := range tests {
		if got := mapParseTaskError(input); got != parseTaskErrCodeFFmpegMissing {
			t.Errorf("mapParseTaskError(%q) = %q, want %q", input, got, parseTaskErrCodeFFmpegMissing)
		}
	}
}
