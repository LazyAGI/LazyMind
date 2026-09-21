package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"lazymind/core/common/orm"
	"strings"
	"testing"
)

func TestDocumentSourceContextProjectsTrustedMetadata(t *testing.T) {
	source := "![image](image.png)"
	sum := sha256.Sum256([]byte(source))
	var response map[string]any
	_ = json.Unmarshal([]byte(descriptorMarkdown), &response)
	response["render_context"] = map[string]any{"source_hash": hex.EncodeToString(sum[:]), "code_fences": []any{}, "images": []any{map[string]any{"start": 0, "end": len(source), "width": 300}}}
	rawResponse, _ := json.Marshal(response)
	spy := descriptorAlgorithm(t, 200, string(rawResponse))
	f := newDescriptorFixture(t)
	f.seed(t, "descriptor-artifact", "unknown-slot", "text/markdown", `{"data":"![image](image.png)","schema":"text/markdown","meta":{"lazymind_provider_sync":{"provider":"obsidian","target_document":{"adapter":"obsidian","uri":"obsidian://fixture/note.md","meta":{"secret":"never-project","obsidian_bridge":{"images":{"image.png":{"resource_uri":"file:///private/not-for-browser","raw_variants":["![[image.png|300]]"]}}}}}}}}`)
	for _, route := range descriptorRoutes {
		for _, record := range descriptorRecords(t, f.read(context.Background(), route, "descriptor-owner")) {
			doc, ok := record["document"].(map[string]any)
			if !ok || doc["render_context"] == nil {
				t.Fatalf("missing render context on %s: %#v", route, record)
			}
		}
	}
	for _, call := range spy.calls() {
		var input map[string]any
		if err := json.Unmarshal(call["artifact"], &input); err != nil {
			t.Fatalf("context not sent: %s", call["artifact"])
		}
		if input["meta"] == nil {
			t.Fatalf("missing source context: %s", call["artifact"])
		}
		if strings.Contains(string(call["artifact"]), "never-project") || strings.Contains(string(call["artifact"]), "file://") {
			t.Fatal("private provider fields escaped projection")
		}
	}
}

func TestDocumentSourceContextRejectsStaleAlgorithmHints(t *testing.T) {
	response := strings.TrimSuffix(descriptorMarkdown, "}") + `,"render_context":{"source_hash":"wrong-source","code_fences":[],"images":[{"start":0,"end":1,"width":300}]}}`
	descriptorAlgorithm(t, 200, response)
	f := newDescriptorFixture(t)
	f.seed(t, "descriptor-artifact", "unknown-slot", "text/markdown", `{"text":"Plain"}`)
	records := descriptorRecords(t, f.read(context.Background(), descriptorRoutes[5], "descriptor-owner"))
	if records[0]["document_error"] == nil {
		t.Fatal("stale algorithm display hints accepted")
	}
}

func TestDocumentSourceContextUsesImportedWriterTarget(t *testing.T) {
	spy := descriptorAlgorithm(t, 200, descriptorMarkdown)
	f := newDescriptorFixture(t)
	if err := f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Updates(&orm.WorkflowSession{WorkflowID: "writer-workflow"}).Error; err != nil {
		t.Fatal(err)
	}
	f.seed(t, "descriptor-artifact", "source_document", "text/markdown", `{"text":"![image](image.png)"}`)
	f.seed(t, "source-target", "target_document", "json", `{"data":{"adapter":"obsidian","meta":{"obsidian_bridge":{"images":{"image.png":{"raw_variants":["![[image.png|300]]"]}}}}}}`)
	f.read(context.Background(), descriptorRoutes[5], "descriptor-owner")
	calls := spy.calls()
	if len(calls) != 1 || !strings.Contains(string(calls[0]["artifact"]), "writer_source") {
		t.Fatalf("imported source lacks display context: %v", calls)
	}
}
