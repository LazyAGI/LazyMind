package common

import "net/http"

func init() {
	registerAdditionalErrorAlias("provision knowledge base", "Upstream service error", http.StatusBadGateway, 2000110)
}
