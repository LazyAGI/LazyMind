//go:build !windows

package main

import "os/exec"

func configureRunnerProcess(_ *exec.Cmd) {}
