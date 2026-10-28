package common

import "testing"

func TestAdvancedRequestErrorsUseSpecificSafeMessages(t *testing.T) {
	for _, fixture := range []struct {
		source       string
		status, code int
	}{
		{"invalid advanced request configuration", 400, 2005050},
		{"invalid request configuration", 400, 2005050},
		{"authentication, protocol and transport headers are managed by LazyMind", 400, 2005051},
		{"model, input, output format and streaming fields are managed by LazyMind", 400, 2005052},
		{"read advanced request configuration failed", 500, 2005053},
		{"request configuration exceeds credential limits", 400, 2005054},
		{"model settings exceed size limit", 400, 2005060},
		{"invalid model settings", 400, 2005061},
		{"model features require a chat model", 400, 2005062},
		{"cannot enable a feature declared unsupported", 400, 2005063},
		{"vision model type requires vision support", 400, 2005064},
		{"embedding options require a text embedding model", 400, 2005065},
		{"embedding prefix exceeds 2048 characters", 400, 2005066},
		{"embedding batch size must be between 1 and 2048", 400, 2005067},
	} {
		resolved := ResolveAppError(fixture.source, fixture.status)
		if resolved.Code != fixture.code || resolved.HTTPStatus != fixture.status || resolved.Detail != nil {
			t.Fatalf("request error mapped incorrectly: %s -> %#v", fixture.source, resolved)
		}
	}
}
