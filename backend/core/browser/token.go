package browser

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type toolClaims struct {
	Subject string `json:"sub"`
	Scope   string `json:"scope"`
	Expires int64  `json:"exp"`
}

type tokenSigner struct {
	secret []byte
	now    func() time.Time
}

func newTokenSigner(secret []byte) (*tokenSigner, error) {
	if len(secret) == 0 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, fmt.Errorf("generate browser token secret: %w", err)
		}
	}
	if len(secret) < 32 {
		return nil, errors.New("browser token secret must be at least 32 bytes")
	}
	return &tokenSigner{secret: append([]byte(nil), secret...), now: time.Now}, nil
}

func (s *tokenSigner) issue(userID string, ttl time.Duration) (string, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "", errors.New("browser tool token user is required")
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	// Hour buckets keep the runtime MCP configuration stable long enough for the
	// Chat service's schema cache while still bounding token lifetime.
	expires := s.now().UTC().Add(ttl).Truncate(time.Hour)
	claims := toolClaims{Subject: userID, Scope: "browser.use", Expires: expires.Unix()}
	raw, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(encoded))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded + "." + signature, nil
}

func (s *tokenSigner) verify(token string) (toolClaims, error) {
	var claims toolClaims
	encoded, signature, ok := strings.Cut(strings.TrimSpace(token), ".")
	if !ok || encoded == "" || signature == "" {
		return claims, errors.New("invalid browser tool token")
	}
	provided, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return claims, errors.New("invalid browser tool token")
	}
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(encoded))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return claims, errors.New("invalid browser tool token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || json.Unmarshal(raw, &claims) != nil {
		return toolClaims{}, errors.New("invalid browser tool token")
	}
	if strings.TrimSpace(claims.Subject) == "" || claims.Scope != "browser.use" {
		return toolClaims{}, errors.New("invalid browser tool token")
	}
	if claims.Expires <= s.now().UTC().Unix() {
		return toolClaims{}, errors.New("browser tool token expired")
	}
	return claims, nil
}
