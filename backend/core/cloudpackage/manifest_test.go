package cloudpackage

import (
	"os"
	"testing"
)

func TestPrepareSkillManifestUsesRootEntrypointAndStableHash(t *testing.T) {
	input := PrepareInput{
		ResourceType:      "skill",
		ResourceName:      "fixture-skill",
		ClientResourceKey: "skill:local-a",
		DesktopVersion:    "1.0.0",
		Files: map[string]File{
			"SKILL.md":       {Data: []byte("---\nname: fixture-skill\n---\n")},
			"scripts/run.py": {Data: []byte("print('ok')\n"), Executable: true},
		},
	}
	first, err := Prepare(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Prepare(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Manifest.Entrypoint != "SKILL.md" || first.Manifest.ContentHash == "" {
		t.Fatalf("manifest=%+v", first.Manifest)
	}
	if first.Manifest.ContentHash != second.Manifest.ContentHash {
		t.Fatalf("content hash is not deterministic: %q != %q", first.Manifest.ContentHash, second.Manifest.ContentHash)
	}
	if first.ZIPPath != "" || second.ZIPPath != "" {
		t.Fatal("Prepare must not create a ZIP before upload decision")
	}
}

func TestPrepareWorkflowExcludesCompiledAndRuntimeState(t *testing.T) {
	prepared, err := Prepare(PrepareInput{
		ResourceType:      "workflow",
		ResourceName:      "fixture-workflow",
		ClientResourceKey: "workflow:local-c",
		DesktopVersion:    "1.0.0",
		Files: map[string]File{
			"workflow.yaml":        {Data: []byte("id: fixture-workflow\nsteps: []\n")},
			"scenario/state.yml":   {Data: []byte("transitions: {}\nsteps: {}\n")},
			"scenario/scenario.md": {Data: []byte("# fixture\n")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Manifest.Entrypoint != "workflow.yaml" {
		t.Fatalf("entrypoint=%q", prepared.Manifest.Entrypoint)
	}
	for _, file := range prepared.Manifest.Files {
		if file.Path == "compiled_graph.json" || file.Path == "session.json" || file.Path == "artifact.json" {
			t.Fatalf("runtime-only file leaked into manifest: %q", file.Path)
		}
	}
}

func TestPrepareRejectsWrappedRootAndTraversal(t *testing.T) {
	for _, files := range []map[string]File{
		{"wrapped/SKILL.md": {Data: []byte("name: wrapped\n")}},
		{"SKILL.md": {Data: []byte("name: bad\n")}, "../secret": {Data: []byte("secret")}},
	} {
		_, err := Prepare(PrepareInput{ResourceType: "skill", ResourceName: "bad", ClientResourceKey: "skill:bad", DesktopVersion: "1.0.0", Files: files})
		if err == nil {
			t.Fatalf("unsafe files accepted: %#v", files)
		}
	}
}

func TestPrepareRejectsMetadataOutsideCloudContract(t *testing.T) {
	for _, input := range []PrepareInput{
		{ResourceType: "skill", ResourceName: "bad/name", ClientResourceKey: "skill:a", DesktopVersion: "1.0.0", Files: map[string]File{"SKILL.md": {Data: []byte("x")}}},
		{ResourceType: "skill", ResourceName: "name", ClientResourceKey: "包含空格", DesktopVersion: "1.0.0", Files: map[string]File{"SKILL.md": {Data: []byte("x")}}},
		{ResourceType: "skill", ResourceName: "name", ClientResourceKey: "skill:a", DesktopVersion: "dev", Files: map[string]File{"SKILL.md": {Data: []byte("x")}}},
	} {
		if _, err := Prepare(input); err == nil {
			t.Fatalf("invalid metadata accepted: %+v", input)
		}
	}
}

func TestWriteZIPIsDeterministicAndFinalizesTransportMetadata(t *testing.T) {
	prepared, err := Prepare(PrepareInput{
		ResourceType:      "skill",
		ResourceName:      "fixture-skill",
		ClientResourceKey: "skill:local-a",
		DesktopVersion:    "1.0.0",
		Files: map[string]File{
			"SKILL.md":  {Data: []byte("name: fixture-skill\n")},
			"refs/a.md": {Data: []byte("a\n")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := WriteZIP(prepared)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(first.ZIPPath)
	second, err := WriteZIP(prepared)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(second.ZIPPath)
	if first.Manifest.TransportHash == "" || first.Manifest.TransportSize <= 0 {
		t.Fatalf("transport metadata=%+v", first.Manifest)
	}
	if first.Manifest.TransportHash != second.Manifest.TransportHash || first.Manifest.TransportSize != second.Manifest.TransportSize {
		t.Fatalf("ZIP is not deterministic: first=%+v second=%+v", first.Manifest, second.Manifest)
	}
}
