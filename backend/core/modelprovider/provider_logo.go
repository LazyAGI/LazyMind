package modelprovider

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image/png"
	"strings"
)

const providerLogoPrefix = "data:image/png;base64,"
const maxProviderLogoBytes = 96 << 10
const maxProviderLogoDimension = 128
const customProviderBodyLimit = 192 << 10

var errInvalidProviderLogo = errors.New("logo_data_url must be a valid PNG of at most 128x128 pixels and 96 KiB")

// normalizeProviderLogo fully decodes and re-encodes a bounded PNG. Only pixel
// data survives; metadata and any appended bytes are never stored or served.
func normalizeProviderLogo(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if !strings.HasPrefix(value, providerLogoPrefix) || len(value) > len(providerLogoPrefix)+base64.StdEncoding.EncodedLen(maxProviderLogoBytes) {
		return "", errInvalidProviderLogo
	}
	encoded := strings.TrimPrefix(value, providerLogoPrefix)
	if strings.ContainsAny(encoded, "\r\n\t ") {
		return "", errInvalidProviderLogo
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(raw) == 0 || len(raw) > maxProviderLogoBytes {
		return "", errInvalidProviderLogo
	}
	config, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > maxProviderLogoDimension || config.Height > maxProviderLogoDimension {
		return "", errInvalidProviderLogo
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", errInvalidProviderLogo
	}
	var canonical bytes.Buffer
	if err := png.Encode(&canonical, img); err != nil || canonical.Len() > maxProviderLogoBytes {
		return "", errInvalidProviderLogo
	}
	return providerLogoPrefix + base64.StdEncoding.EncodeToString(canonical.Bytes()), nil
}
