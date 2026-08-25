//go:build !windows

package mcpclient

import "os"

func replaceConfigFile(source, destination string) error {
	return os.Rename(source, destination)
}
