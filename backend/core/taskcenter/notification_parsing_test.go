package taskcenter

import (
	"encoding/json"
	"testing"
)

func TestNotificationConfigValuePreservesJSONContract(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		missing, invalid  bool
	}{
		{name: "unconfigured", missing: true, want: "null"},
		{name: "legacy null", input: "null", want: `{"events":null,"channels":null}`},
		{name: "empty", input: "{}", want: `{"events":null,"channels":null}`},
		{name: "configured", input: `{"events":{},"channels":{"desktop":{"enabled":true}}}`, want: `{"events":{},"channels":{"desktop":{"enabled":true}}}`},
		{name: "malformed", input: "{", invalid: true},
		{name: "wrong type", input: "[]", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := &tc.input
			if tc.missing {
				raw = nil
			}
			config, err := notificationConfigValue(raw)
			if (err != nil) != tc.invalid {
				t.Fatalf("error = %v, want invalid=%v", err, tc.invalid)
			}
			if tc.invalid {
				return
			}
			encoded, err := json.Marshal(config)
			if err != nil || string(encoded) != tc.want {
				t.Fatalf("JSON = %s, error = %v, want %s", encoded, err, tc.want)
			}
		})
	}
}
