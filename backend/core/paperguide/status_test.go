package paperguide

import (
	"context"
	"testing"
)

type unavailableGuideSource struct {
	SourceLoader
	err error
}

func (s unavailableGuideSource) Load(context.Context, Scope, bool) (SourceState, error) {
	return SourceState{PrecisionUnavailable: true}, s.err
}

func TestDisabledStatusRetainsReasonWhenSourceIsUnavailable(t *testing.T) {
	svc, scope, _, _ := fastFixture(t)
	svc.Authorize = func(_ context.Context, _ Scope, generate bool) error {
		if generate {
			return ErrDisabled
		}
		return nil
	}
	for _, sourceErr := range []error{nil, ErrSourceChanged} {
		svc.Source = unavailableGuideSource{err: sourceErr}
		status, err := svc.Status(t.Context(), scope)
		if err != nil || !status.CanRead || status.CanGenerate || status.UnavailableReason != ErrDisabled.Error() {
			t.Fatalf("status with source error %v: %+v, %v", sourceErr, status, err)
		}
	}
}
