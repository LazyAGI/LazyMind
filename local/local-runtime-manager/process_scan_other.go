//go:build !linux && !darwin

package main

func scanLocalRuntimeProcesses(paths RuntimePaths) ([]LocalProcessRecord, error) {
	return nil, nil
}
