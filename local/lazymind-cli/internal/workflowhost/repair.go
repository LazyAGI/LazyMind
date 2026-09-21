package workflowhost

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

//go:embed repair_log.mjs
var repairLogScript string

// RepairLog never touches history during installation. It is an explicit,
// offline maintenance command; the default invocation only prints a preview.
func RepairLog(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	node, err := exec.LookPath("node")
	if err != nil {
		return fmt.Errorf("DSH session repair requires Node.js 24 or newer: %w", err)
	}
	command := exec.CommandContext(ctx, node, append([]string{"--input-type=module", "-"}, args...)...)
	command.Stdin = strings.NewReader(repairLogScript)
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
}
