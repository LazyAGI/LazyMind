//go:build !linux && !darwin && !windows

package credentialvault

func newSystemLocalKeyStore(string) LocalKeyStore { return unavailableLocalKeyStore{} }
