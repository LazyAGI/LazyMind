package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestUnconfiguredHelperIsExplicitlyDisabled(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"config"}, strings.NewReader(""), &out, "", t.TempDir(), time.Now()); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["status"] != "disabled" || result["reason"] != "not_configured" {
		t.Fatalf("result=%v", result)
	}
}

func TestCLIRejectsRuntimeConfigurationOverrides(t *testing.T) {
	for _, args := range [][]string{{"config", "--config", "attacker.json"}, {"check", "--trust-root", "/tmp/alternate"}, {"install"}} {
		if err := run(context.Background(), args, strings.NewReader(""), &bytes.Buffer{}, "", t.TempDir(), time.Now()); err == nil {
			t.Fatalf("unsupported invocation accepted: %v", args)
		}
	}
	if err := run(context.Background(), []string{"config"}, strings.NewReader(""), &bytes.Buffer{}, "invalid-base64", t.TempDir(), time.Now()); err == nil {
		t.Fatal("invalid packaged config treated as disabled")
	}
}
