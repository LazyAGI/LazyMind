package secretcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
)

type Wrapper struct {
	Enc   string `json:"enc"`
	Nonce string `json:"nonce"`
	V     string `json:"v"`
}

func EncodeAESGCM(plaintext []byte, key string) (json.RawMessage, error) {
	aead, err := aeadFromKey(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return json.Marshal(Wrapper{
		Enc:   "aes-gcm",
		Nonce: base64.StdEncoding.EncodeToString(nonce),
		V:     base64.StdEncoding.EncodeToString(aead.Seal(nil, nonce, plaintext, nil)),
	})
}

func DecodeAESGCM(raw json.RawMessage, key string) ([]byte, bool, error) {
	var wrapper Wrapper
	if json.Unmarshal(raw, &wrapper) != nil || wrapper.Enc != "aes-gcm" || wrapper.V == "" {
		return nil, false, nil
	}
	nonce, err := base64.StdEncoding.DecodeString(wrapper.Nonce)
	if err != nil {
		return nil, true, err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(wrapper.V)
	if err != nil {
		return nil, true, err
	}
	aead, err := aeadFromKey(key)
	if err != nil {
		return nil, true, err
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	return plaintext, true, err
}

func aeadFromKey(key string) (cipher.AEAD, error) {
	sum := sha256.Sum256([]byte(strings.TrimSpace(key)))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
