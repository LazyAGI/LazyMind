// A native-process fixture for the PowerShell launcher contract tests.
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	executable, _ := os.Executable()
	log, err := os.OpenFile(os.Getenv("BRIDGE_PROBE_LOG"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		panic(err)
	}
	_ = json.NewEncoder(log).Encode(map[string]any{
		"args": os.Args[1:], "executable": executable,
		"home": os.Getenv("LAZYMIND_HOME"), "buildRoot": os.Getenv("LAZYMIND_LOCAL_BUILD_ROOT"),
	})
	_ = log.Close()
	action, mode := os.Args[2], os.Getenv("BRIDGE_PROBE_MODE")
	if mode == "fail-"+action {
		// 0xFFFD0000 has zero low eight bits, but must remain a failure.
		os.Exit(-196608)
	}
	if action != "status" {
		fmt.Println("native command completed")
		return
	}
	switch mode {
	case "invalid-json":
		fmt.Println("not JSON")
	case "not-running":
		fmt.Println(`{"running":false,"platform":"windows"}`)
	case "wrong-platform":
		fmt.Println(`{"running":true,"platform":"linux"}`)
	case "string-running":
		fmt.Println(`{"running":"true","platform":"windows"}`)
	default:
		fmt.Println(`{"running":true,"platform":"windows"}`)
	}
}
