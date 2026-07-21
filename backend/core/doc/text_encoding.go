package doc

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"lazymind/core/common"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

const uploadTextUTF8ConvertEnv = "LAZYMIND_UPLOAD_TEXT_UTF8_CONVERT_ENABLED"

func uploadTextUTF8ConvertEnabled() bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(uploadTextUTF8ConvertEnv)))
	switch raw {
	case "", "1", "true", "yes", "on", "enabled":
		return true
	case "0", "false", "no", "off", "disabled":
		return false
	default:
		return true
	}
}

func shouldNormalizeUploadedTextFile(path, originalFilename string) bool {
	name := strings.TrimSpace(originalFilename)
	if name == "" {
		name = filepath.Base(path)
	}
	return common.IsTextFileExtension(filepath.Ext(name))
}

func normalizeUploadedTextFileInPlace(path, originalFilename string, currentSize int64) (int64, error) {
	if !uploadTextUTF8ConvertEnabled() || !shouldNormalizeUploadedTextFile(path, originalFilename) {
		return currentSize, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read uploaded text file: %w", err)
	}
	if utf8.Valid(data) {
		return int64(len(data)), nil
	}

	decoded, err := decodeUploadedTextToUTF8(data)
	if err != nil {
		return 0, err
	}
	if err := replaceFileAtomically(path, decoded); err != nil {
		return 0, err
	}
	return int64(len(decoded)), nil
}

func decodeUploadedTextToUTF8(data []byte) ([]byte, error) {
	for _, decoder := range uploadTextDecoders() {
		decoded, err := io.ReadAll(transform.NewReader(bytes.NewReader(data), decoder.NewDecoder()))
		if err == nil && utf8.Valid(decoded) {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("uploaded text file encoding cannot be converted to UTF-8")
}

func uploadTextDecoders() []encoding.Encoding {
	return []encoding.Encoding{
		unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM),
		unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM),
		simplifiedchinese.GB18030,
		simplifiedchinese.GBK,
		traditionalchinese.Big5,
		japanese.ShiftJIS,
		japanese.EUCJP,
		korean.EUCKR,
	}
}

func replaceFileAtomically(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat uploaded text file: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".utf8-*")
	if err != nil {
		return fmt.Errorf("create UTF-8 temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write UTF-8 temp file: %w", err)
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod UTF-8 temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close UTF-8 temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace uploaded text file: %w", err)
	}
	return nil
}
