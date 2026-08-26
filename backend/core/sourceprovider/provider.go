package sourceprovider

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const maxNameRunes = 64

type validationError string

func (err validationError) Error() string { return string(err) }

func failure(format string, args ...any) error {
	return validationError(fmt.Sprintf(format, args...))
}

func Normalize(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if strings.ContainsAny(name, "\r\n\t") {
		return "", failure("provider must be a single-line label")
	}
	if utf8.RuneCountInString(name) > maxNameRunes {
		return "", failure("provider exceeds %d characters", maxNameRunes)
	}
	return name, nil
}
