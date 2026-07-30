package contract

import (
	"errors"
	"strings"
	"testing"
)

func TestErrorUnwrapPreservesCauseWithoutExposingItInMessage(t *testing.T) {
	cause := errors.New("database secret leaked")
	err := NewError(BackendUnavailable, "skill.list", "backend unavailable", true, cause)

	if !errors.Is(err, cause) {
		t.Fatalf("errors.Is did not match wrapped cause")
	}
	if strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("Error() = %q, want no cause text", err.Error())
	}
}
