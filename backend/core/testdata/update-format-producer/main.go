package main

import (
	"fmt"
	"os"

	"lazymind/core/internal/updateformattest"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: update-format-producer <database path>")
		os.Exit(2)
	}
	if err := updateformattest.CreateCoreDatabase(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
