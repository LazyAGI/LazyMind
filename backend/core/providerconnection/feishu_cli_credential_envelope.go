package providerconnection

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"io"
)

type feishuCLISealedCredential struct {
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

func feishuCLICredentialCipher(key []byte) (cipher.AEAD, error) {
	if len(key) < 32 {
		return nil, ErrCLIUnavailable
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("lazymind-feishu-cli-user-token-response-v1"))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, ErrCLIUnavailable
	}
	return cipher.NewGCM(block)
}

// SealFeishuCLIUserToken protects the short-lived credential even on the
// existing HTTP Sidecar network. The request signature binds owner, connection,
// profile, capability and request nonce without exposing another plaintext key.
func SealFeishuCLIUserToken(key []byte, signature string, token ResolvedToken) ([]byte, error) {
	if len(signature) != sha256.Size*2 {
		return nil, ErrCLIUnavailable
	}
	aead, err := feishuCLICredentialCipher(key)
	if err != nil {
		return nil, err
	}
	plain, err := json.Marshal(token)
	if err != nil || len(plain) > 16<<10 {
		return nil, ErrCLIOutputInvalid
	}
	defer func() {
		for i := range plain {
			plain[i] = 0
		}
	}()
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, ErrCLIUnavailable
	}
	return json.Marshal(feishuCLISealedCredential{Nonce: nonce, Ciphertext: aead.Seal(nil, nonce, plain, []byte(signature))})
}

func openFeishuCLIUserToken(key []byte, signature string, payload []byte) ([]byte, error) {
	var sealed feishuCLISealedCredential
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&sealed) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, ErrCLIOutputInvalid
	}
	aead, err := feishuCLICredentialCipher(key)
	if err != nil {
		return nil, err
	}
	if len(sealed.Nonce) != aead.NonceSize() {
		return nil, ErrCLIOutputInvalid
	}
	plain, err := aead.Open(nil, sealed.Nonce, sealed.Ciphertext, []byte(signature))
	if err != nil {
		return nil, ErrCLIOutputInvalid
	}
	return plain, nil
}
