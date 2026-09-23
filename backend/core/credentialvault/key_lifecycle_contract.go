package credentialvault

func (cache *ManifestCache) KeyByID(keyID string) (ShardPublicKey, error) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	for _, key := range cache.manifest.Keys {
		if key.KeyID == keyID && (key.Status == "active" || key.Status == "retiring") {
			return key, nil
		}
	}
	return ShardPublicKey{}, ErrInvalidContract
}
