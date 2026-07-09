package resourcefs

import (
	"strings"
)

const MemoryPath = "memory/memory.md"
const UserPreferencePath = "memory/user.md"

func FixedPath(resourceType ResourceType) (string, error) {
	switch resourceType {
	case ResourceTypeMemory:
		return MemoryPath, nil
	case ResourceTypeUserPreference:
		return UserPreferencePath, nil
	default:
		return "", ErrInvalidResourceType
	}
}

func NormalizePath(path string) string {
	return strings.TrimLeft(strings.TrimSpace(path), "/")
}

func ResourceTypeForPath(path string) (ResourceType, error) {
	switch NormalizePath(path) {
	case MemoryPath:
		return ResourceTypeMemory, nil
	case UserPreferencePath:
		return ResourceTypeUserPreference, nil
	default:
		return "", ErrInvalidPath
	}
}

func IsPersonalResourcePath(path string) bool {
	_, err := ResourceTypeForPath(path)
	return err == nil || NormalizePath(path) == "memory"
}
