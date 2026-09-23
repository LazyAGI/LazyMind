package workflowhost

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestOfflineLogRepairIsPreviewFirstAndPreservesOtherEvents(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is unavailable")
	}
	if err := exec.Command(node, "--input-type=module", "-e", "import{zstdDecompressSync}from'node:zlib';if(!zstdDecompressSync)process.exit(1)").Run(); err != nil {
		t.Skip("Node.js 24 is required")
	}
	file := filepath.Join(t.TempDir(), "session.jsonl")
	original := []byte("{\"type\":\"header\",\"id\":\"session-test\"}\n{\"type\":\"user/message\",\"seq\":1,\"data\":{}}\n{\"type\":\"lazymind-workflow/open\",\"seq\":2,\"data\":{\"runId\":\"run\"}}\n")
	if err := os.WriteFile(file, original, 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if err := RepairLog(context.Background(), []string{"--file", file}, &out, &stderr); err != nil {
		t.Fatal(err, stderr.String())
	}
	after, _ := os.ReadFile(file)
	if !bytes.Equal(original, after) {
		t.Fatal("preview modified the log")
	}
	if err := RepairLog(context.Background(), []string{"--file", file, "--apply"}, &out, &stderr); err == nil {
		t.Fatal("online repair was allowed")
	}
	out.Reset()
	stderr.Reset()
	if err := RepairLog(context.Background(), []string{"--file", file, "--apply", "--offline"}, &out, &stderr); err != nil {
		t.Fatal(err, stderr.String())
	}
	var report struct {
		Backup string `json:"backup"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	backup, _ := os.ReadFile(report.Backup)
	if !bytes.Equal(original, backup) {
		t.Fatal("backup lost original bytes")
	}
	after, _ = os.ReadFile(file)
	rows := bytes.Split(after, []byte("\n"))
	if !bytes.Equal(rows[0], bytes.Split(original, []byte("\n"))[0]) || !bytes.Equal(rows[1], bytes.Split(original, []byte("\n"))[1]) {
		t.Fatal("unrelated records changed")
	}
	if !bytes.Contains(rows[2], []byte(`"ignorable":true`)) {
		t.Fatal("missing repaired marker")
	}
	out.Reset()
	if err := RepairLog(context.Background(), []string{"--file", file, "--apply", "--offline"}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	repeated, _ := os.ReadFile(file)
	if !bytes.Equal(after, repeated) {
		t.Fatal("repeat repair changed the log")
	}
}

func TestOfflineLogRepairReadsEveryCompressedFrame(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is unavailable")
	}
	file := filepath.Join(t.TempDir(), "session.jsonl.zstd")
	fixture := `const fs=require('node:fs'),z=require('node:zlib');if(!z.zstdCompressSync)process.exit(24);fs.writeFileSync(process.argv[1],Buffer.concat([z.zstdCompressSync(Buffer.from(JSON.stringify({type:'header',id:'session-test'})+'\n')),z.zstdCompressSync(Buffer.from(JSON.stringify({type:'lazymind-workflow/open',seq:2,data:{runId:'run'}})+'\n'))]));`
	if err := exec.Command(node, "-e", fixture, file).Run(); err != nil {
		t.Skip("Node.js 24 is required")
	}
	original, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if err := RepairLog(context.Background(), []string{"--file", file, "--apply", "--offline"}, &out, &stderr); err != nil {
		t.Fatal(err, stderr.String())
	}
	var report struct {
		Backup  string `json:"backup"`
		Changed []any  `json:"changed_records"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Changed) != 1 {
		t.Fatal("only decoded the header frame", out.String())
	}
	backup, _ := os.ReadFile(report.Backup)
	if !bytes.Equal(original, backup) {
		t.Fatal("compressed backup differs")
	}
	out.Reset()
	if err := RepairLog(context.Background(), []string{"--file", file}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || len(report.Changed) != 0 {
		t.Fatal("repaired stream does not replay", out.String())
	}
}
