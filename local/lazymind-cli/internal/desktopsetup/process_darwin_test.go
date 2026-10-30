package desktopsetup

import "testing"

func TestListenerProcessesMatch(t *testing.T) {
	const app = "/Applications/ChatGPT.app"
	const main = "100 1 /Applications/ChatGPT.app/Contents/MacOS/ChatGPT\n"
	const helper = "200 100 /Users/test/.codex/computer-use/Codex Computer Use.app/Contents/MacOS/SkyComputerUseService\n"
	for _, tc := range []struct {
		name      string
		listeners []string
		snapshot  string
		want      bool
	}{
		{"main", []string{"100"}, main, true},
		{"inherited listener outside bundle", []string{"100", "200"}, main + helper, true},
		{"nested helper", []string{"100", "300"}, main + helper + "300 200 /tmp/helper\n", true},
		{"unrelated listener", []string{"100", "200"}, main + "200 1 /tmp/other-app\n", false},
		{"only helper remains", []string{"200"}, main + helper, false},
		{"exited process", []string{"100", "200"}, main, false},
		{"cycle", []string{"100", "200"}, main + "200 300 /tmp/helper\n300 200 /tmp/helper2\n", false},
		{"similar bundle name", []string{"100"}, "100 1 /Applications/ChatGPT.app-other/Contents/MacOS/ChatGPT\n", false},
		{"no listener", nil, main, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := listenerProcessesMatch(app, tc.listeners, tc.snapshot); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
