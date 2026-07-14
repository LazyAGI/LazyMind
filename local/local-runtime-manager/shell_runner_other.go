//go:build !windows

package main

import (
	"context"
	"fmt"
	"io"
)

func runProcessComposeShell(context.Context, []string, io.Writer, io.Writer) error {
	return fmt.Errorf("the process-compose shell wrapper is only available on Windows")
}
