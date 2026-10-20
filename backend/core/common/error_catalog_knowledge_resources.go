package common

import "net/http"

func init() {
	registerAdditionalErrorAlias("provision knowledge base", "Upstream service error", http.StatusBadGateway, 2000110)
	registerAdditionalErrorAlias("algorithm catalog unavailable", "Upstream service error", http.StatusBadGateway, 2000110)
	registerAdditionalErrorAlias("unknown knowledge scene", "Invalid request", http.StatusBadRequest, 2000103)
}
