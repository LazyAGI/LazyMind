package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
)

type Command struct {
	Name string
	Args []string
	Dir  string
}

type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type CommandRunner interface {
	Run(ctx context.Context, cmd Command) (CommandResult, error)
}

type ExecRunner struct{}

func (r *ExecRunner) Run(ctx context.Context, cmd Command) (CommandResult, error) {
	c := exec.CommandContext(ctx, cmd.Name, cmd.Args...)
	c.Dir = cmd.Dir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	err := c.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	return CommandResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, err
}

func (r *ExecRunner) String() string {
	return "exec"
}

func formatCommand(name string, args []string) string {
	return fmt.Sprintf("%s %v", name, args)
}
