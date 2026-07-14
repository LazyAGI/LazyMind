//go:build !windows

package main

func relocateDesktopPythonVenvs(_ RuntimeConfig, _ RuntimePaths) error {
	return nil
}
