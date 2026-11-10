package agentexec

import "testing"

func TestDSHProcessSnapshotRunning(t *testing.T) {
	const contents = "/Applications/DeepSeek Harness.app/Contents"
	for _, test := range []struct {
		name, command string
		running       bool
	}{
		{"main", contents + "/MacOS/DeepSeek Harness", true},
		{"helper remains", contents + "/Frameworks/DeepSeek Harness Helper.app/Contents/MacOS/DeepSeek Harness Helper --type=renderer", true},
		{"inspection", contents + "/MacOS/DeepSeek Harness --expose-internals -e inspect", false},
		{"unrelated", "/Applications/Another.app/Contents/MacOS/DeepSeek Harness", false},
		{"similar path", contents + " Other/MacOS/DeepSeek Harness", false},
		{"exited", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := dshProcessSnapshotRunning(contents, test.command); got != test.running {
				t.Fatalf("running=%v, want %v", got, test.running)
			}
		})
	}
}
