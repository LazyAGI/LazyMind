//go:build !linux && !darwin && !windows

package main

func scanLocalRuntimeProcesses(paths RuntimePaths) ([]LocalProcessRecord, error) {
	return nil, nil
}
