// A standalone, cross-platform CLI/helper fixture for real startup tests.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	args := os.Args[1:]
	encode := func(value any) { _ = json.NewEncoder(os.Stdout).Encode(value) }
	if len(args) == 0 {
		encode(map[string]string{"access_token": "fixture-startup-access", "open_id": "ou_fixture", "tenant_key": "fixture-tenant"})
		return
	}
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("lark-cli version 1.0.93")
		return
	}
	if len(args) >= 2 && args[0] == "auth" && args[1] == "status" {
		encode(map[string]any{"identity": "user", "verified": true, "identities": map[string]any{"user": map[string]any{"available": true, "openId": "ou_fixture"}}})
		return
	}
	if len(args) >= 5 && args[0] == "auth" && args[1] == "check" && args[2] == "--scope" {
		encode(map[string]any{"ok": true, "granted": strings.Fields(args[3]), "missing": []string{}})
		return
	}
	if len(args) >= 3 && args[0] == "api" && args[1] == "GET" {
		encode(map[string]any{"ok": true, "identity": "user", "data": map[string]string{"content": "fixture read content", "open_id": "ou_fixture", "tenant_key": "fixture-tenant"}})
		return
	}
	os.Exit(1)
}
