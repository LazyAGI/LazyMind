package codexruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsMSIXDesktopLayout(t *testing.T) {
	for _, tc := range []struct {
		name, identity, entries string
		remove, extra           string
		wantError               bool
	}{
		{"desktop-and-launcher", "OpenAI.Codex", `<Application Executable="app/ChatGPT.exe"/><Application Executable="app/resources/codex-command-runner.exe"/>`, "", "", false},
		{"backslash-entry", "OpenAI.Codex", `<Application Executable="app\ChatGPT.exe"/>`, "", "", false},
		{"ambiguous-manifest", "OpenAI.Codex", `<Application Executable="app/ChatGPT.exe"/><Application Executable="app/Codex.exe"/>`, "", "", true},
		{"unrelated-package", "Other.Codex", `<Application Executable="app/ChatGPT.exe"/>`, "", "", true},
		{"unrelated-entry", "OpenAI.Codex", `<Application Executable="app/resources/codex-command-runner.exe"/>`, "", "", true},
		{"escaping-entry", "OpenAI.Codex", `<Application Executable="../ChatGPT.exe"/>`, "", "", true},
		{"absolute-entry", "OpenAI.Codex", `<Application Executable="C:\ChatGPT.exe"/>`, "", "", true},
		{"malformed-manifest", "OpenAI.Codex", `<Application`, "", "", true},
		{"missing-desktop", "OpenAI.Codex", `<Application Executable="app/ChatGPT.exe"/>`, "ChatGPT.exe", "", true},
		{"missing-node", "OpenAI.Codex", `<Application Executable="app/ChatGPT.exe"/>`, "resources/cua_node/bin/node.exe", "", true},
		{"missing-cli", "OpenAI.Codex", `<Application Executable="app/ChatGPT.exe"/>`, "resources/codex.exe", "", true},
		{"ambiguous-node", "OpenAI.Codex", `<Application Executable="app/ChatGPT.exe"/>`, "", "resources/cua_node/node.exe", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			app := filepath.Join(root, "app")
			for _, relative := range append([]string{"ChatGPT.exe", "Codex.exe", "resources/codex.exe", "resources/codex-command-runner.exe", "resources/cua_node/bin/node.exe"}, tc.extra) {
				if relative == "" || relative == tc.remove {
					continue
				}
				file := filepath.Join(app, filepath.FromSlash(relative))
				if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			manifest := `<Package xmlns="http://schemas.microsoft.com/appx/manifest/foundation/windows10"><Identity Name="` + tc.identity + `"/><Applications>` + tc.entries + `</Applications></Package>`
			if err := os.WriteFile(filepath.Join(root, "AppxManifest.xml"), []byte(manifest), 0600); err != nil {
				t.Fatal(err)
			}
			actual, node, err := windowsDesktopLayout(filepath.Join(app, "resources", "codex.exe"))
			if tc.wantError {
				if err == nil {
					t.Fatal("accepted invalid layout")
				}
				return
			}
			if err != nil || actual != app || node != filepath.Join(app, "resources", "cua_node", "bin", "node.exe") {
				t.Fatalf("app=%q node=%q err=%v", actual, node, err)
			}
			// The Node prepare stage uses this exact native operation, so test
			// its JSON contract as well as the initial Go installation check.
			request, _ := json.Marshal(nativeRequest{Path: app})
			var output bytes.Buffer
			if err := RunNative(context.Background(), []string{"windows-desktop-executable"}, bytes.NewReader(request), &output); err != nil {
				t.Fatal(err)
			}
			var desktop string
			if err := json.Unmarshal(output.Bytes(), &desktop); err != nil || desktop != filepath.Join(app, "ChatGPT.exe") {
				t.Fatalf("native desktop=%q err=%v", desktop, err)
			}
			if _, _, err := windowsDesktopLayout(filepath.Join(app, "Codex.exe")); err == nil {
				t.Fatal("accepted desktop launcher as CLI")
			}
		})
	}
}
