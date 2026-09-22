package main

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"sync"
)

var runtimeCredentialState = struct {
	sync.Once
	internalServiceToken string
	clientInstanceID     string
}{}

func internalServiceToken() string {
	runtimeCredentialState.Do(initializeRuntimeCredentials)
	return runtimeCredentialState.internalServiceToken
}

func runtimeClientInstanceID() string {
	runtimeCredentialState.Do(initializeRuntimeCredentials)
	return runtimeCredentialState.clientInstanceID
}

func initializeRuntimeCredentials() {
	runtimeCredentialState.internalServiceToken = strings.TrimSpace(envText("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", ""))
	if runtimeCredentialState.internalServiceToken == "" {
		runtimeCredentialState.internalServiceToken = randomRuntimeCredential(32)
	}
	runtimeCredentialState.clientInstanceID = strings.TrimSpace(envText("LAZYMIND_CLIENT_INSTANCE_ID", ""))
	if runtimeCredentialState.clientInstanceID == "" {
		runtimeCredentialState.clientInstanceID = "ci_" + randomRuntimeCredential(24)
	}
}

func randomRuntimeCredential(size int) string {
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		panic("secure runtime credential generation failed")
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}
