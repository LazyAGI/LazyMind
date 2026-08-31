//go:build !windows && !darwin

package agentexec

func platformExecutableCandidates([]string) []string { return nil }

func resolvePlatformExecutable(string) (string, bool, error) { return "", false, nil }

func platformSafeEnvironment(environment []string) []string { return environment }
