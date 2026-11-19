package metadata

// NormalizeExternalDocument applies the same narrow compatibility repairs to
// every URL import. Unknown fields, body text, and original long values remain
// in the document. A replacement runtime name requires a verified source slug.
func NormalizeExternalDocument(content []byte, canonicalName string) ([]byte, []NormalizationWarning, error) {
	content, _, err := NormalizeExternalDescription(content)
	if err != nil {
		return nil, nil, err
	}
	return NormalizeExternalMetadata(content, canonicalName)
}
