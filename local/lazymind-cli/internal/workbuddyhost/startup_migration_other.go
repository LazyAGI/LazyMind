//go:build !darwin

package workbuddyhost

import "context"

// Windows already persists the environment in the user's registry.
func migrateDesktopStartup(context.Context, string, string) error { return nil }
