package workflowmcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/connectorconfig"
)

func TestMarkdownReferencesIgnoreCodeAndNonImageLinks(t *testing.T) {
	text := "![inline](images/a(b).png)\n![reference][ref]\n[ref]: <images/图 1.png>\n`![fake](private.png)`\n```md\n![fake](secret.png)\n```\n[link](private.txt)\n![remote](https://example.test/a.png)"
	want := []string{"images/a(b).png", "images/图 1.png"}
	if got := markdownImageReferences(text); !reflect.DeepEqual(got, want) {
		t.Fatalf("references=%q want=%q", got, want)
	}
}

func TestMarkdownInputCollectsImagesAndNeverFailsMainImport(t *testing.T) {
	t.Setenv(connectorconfig.EnvironmentKey, "")
	for _, deny := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "image-import-fails"}[deny], func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "material.md")
			text := "# Original\n\n![figure](figure.png)\n![missing](missing.png)\n"
			if err := os.WriteFile(source, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
			if err := os.WriteFile(filepath.Join(root, "figure.png"), png, 0600); err != nil {
				t.Fatal(err)
			}
			var saved []byte
			imageCalls := 0
			prepared := false
			client := readinessClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/core/workflow-preparations":
					var request struct {
						InputBindings map[string][]map[string]any `json:"input_bindings"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					items := request.InputBindings["source_files"]
					if len(items) != 1 || items[0]["resource_id"] != "document-resource" {
						t.Errorf("MD not bound: %+v", request)
					}
					prepared = true
					_ = json.NewEncoder(w).Encode(map[string]any{"preparation_id": "prep"})
					return
				case "/api/core/workflow-preparations/prep:consume":
					_ = json.NewEncoder(w).Encode(map[string]any{"session_id": "writer-run", "workflow_id": "writer-workflow"})
					return
				case "/api/core/workflow-sessions/writer-run/projection":
					_ = json.NewEncoder(w).Encode(map[string]any{"session_id": "writer-run", "state_version": 1})
					return
				}
				var input struct {
					Name string `json:"name"`
					Data string `json:"content_base64"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Fatal(err)
				}
				if input.Name == "figure.png" {
					imageCalls++
					if deny {
						http.Error(w, "unavailable", 403)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"resource_id": "image-resource", "revision": 1})
					return
				}
				saved, _ = base64.StdEncoding.DecodeString(input.Data)
				_ = json.NewEncoder(w).Encode(map[string]any{"resource_id": "document-resource", "revision": 1})
			})
			client.HostProvider = "codex"
			result := inputToolCall(t, inputToolSession(t, client), "workflow.start", map[string]any{
				"workflow_id": "writer-workflow", "request_context": "基于附件扩写，保留原图",
				"inputs": map[string]any{"source_files": []any{map[string]any{"local_path": source}}},
			})
			if result.IsError || !prepared {
				t.Fatalf("main import/binding failed: %+v", result.Content)
			}
			if imageCalls != 1 || !bytes.HasPrefix(saved, []byte(text)) || !bytes.Contains(saved, []byte("lazymind-markdown-images:v1:")) {
				t.Fatalf("bad snapshot: calls=%d snapshot=%s", imageCalls, saved)
			}
			encoded := strings.Split(strings.Split(string(saved), "lazymind-markdown-images:v1:")[1], " -->")[0]
			metadata, _ := base64.StdEncoding.DecodeString(encoded)
			var manifest markdownManifest
			if err := json.Unmarshal(metadata, &manifest); err != nil {
				t.Fatal(err)
			}
			if len(manifest.Warnings) == 0 || !deny && len(manifest.Images) != 1 || deny && len(manifest.Images) != 0 {
				t.Fatalf("manifest=%+v", manifest)
			}
			unchanged, _ := os.ReadFile(source)
			if string(unchanged) != text {
				t.Fatal("original Markdown changed")
			}
		})
	}
}

func TestMarkdownImageRejectsTraversalAndNonImage(t *testing.T) {
	t.Setenv(connectorconfig.EnvironmentKey, "")
	root := t.TempDir()
	dir := filepath.Join(root, "docs")
	_ = os.Mkdir(dir, 0700)
	source := filepath.Join(dir, "source.md")
	_ = os.WriteFile(source, []byte("# Source"), 0600)
	_ = os.WriteFile(filepath.Join(root, "outside.png"), []byte("image"), 0600)
	_ = os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("secret"), 0600)
	for _, reference := range []string{"../outside.png", "secret.txt", "."} {
		if _, err := localMarkdownImage(source, reference); err == nil {
			t.Fatalf("unexpected read: %s", reference)
		}
	}
}
