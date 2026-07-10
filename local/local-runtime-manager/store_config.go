package main

import (
	"os"
	"strings"
)

func isBuiltInServiceURI(envName, fallback string) bool {
	v := strings.TrimSpace(os.Getenv(envName))
	if v == "" {
		v = fallback
	}
	return v == fallback || v == fallback+"/"
}

func localSegmentStoreType() string {
	return strings.TrimSpace(envText("LAZYMIND_SEGMENT_STORE_TYPE", "SQLiteStore"))
}

func localSegmentStoreUsesBuiltInOpenSearch() bool {
	return strings.EqualFold(localSegmentStoreType(), "opensearch") &&
		isBuiltInServiceURI("LAZYMIND_SEGMENT_STORE_URI_OR_PATH", "https://opensearch:9200")
}
