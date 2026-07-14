package main

import (
	"errors"
	"os"
)

func attachManagedProcess(paths RuntimePaths, service string, process *os.Process) (func(), error) {
	return attachProcessJob(paths, service, process)
}

func forceStopManagedProcess(paths RuntimePaths, service string, pid int) error {
	if err := terminateProcessJob(paths, service); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) && !processAlive(pid) {
		return nil
	}
	return forceKillProcessTree(pid)
}
