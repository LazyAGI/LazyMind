//go:build windows

package main

import "os/exec"

func configureRunnerProcess(cmd *exec.Cmd) {
	configureChildProcess(cmd, false)
}
