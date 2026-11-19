package httperr

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestImportSizeErrorsReturnPayloadTooLarge(t *testing.T) {
	for _, message := range []string{
		"skill package download exceeds 104857600 bytes",
		`skill package file "image.png" exceeds 33554432 bytes`,
		"skill package exceeds 134217728 uncompressed bytes",
		"skill package contains too many entries: 513 > 512",
		"GitHub API rate limit reached; GitHub archive discovery failed: skill package download exceeds 104857600 bytes",
	} {
		t.Run(message, func(t *testing.T) {
			err := fmt.Errorf("import package: %w", errors.New(message))
			sem := ForError(err)
			if sem.Status != http.StatusRequestEntityTooLarge || sem.Code != CodePayloadTooLarge {
				t.Fatalf("semantic = %#v, want 413 payload_too_large", sem)
			}
			rec := httptest.NewRecorder()
			ReplyError(rec, err)
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("HTTP status = %d, want 413", rec.Code)
			}
		})
	}
}

func TestWrappedSemanticPreservesExplicitCodeAndStatus(t *testing.T) {
	sem := Semantic{Status: http.StatusBadRequest, Code: "github_multiple_skills", Message: "Choose a skill directory"}
	err, ok := any(sem).(error)
	if !ok {
		t.Fatal("Semantic cannot be propagated as an error")
	}
	got := ForError(fmt.Errorf("import failed: %w", err))
	if got.Status != 400 || got.Code != "github_multiple_skills" || got.Message != "import failed: Choose a skill directory" {
		t.Fatalf("wrapped semantic = %#v", got)
	}
}
