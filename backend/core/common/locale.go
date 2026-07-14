package common

import (
	"net/http"
	"strings"
)

const (
	LocaleZhCN = "zh-CN"
	LocaleEnUS = "en-US"
)

// NormalizeLocale maps Accept-Language values to the UI locales supported by Core.
func NormalizeLocale(acceptLanguage string) string {
	for _, part := range strings.Split(acceptLanguage, ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		switch {
		case tag == "zh" || strings.HasPrefix(tag, "zh-"):
			return LocaleZhCN
		case tag == "en" || strings.HasPrefix(tag, "en-"):
			return LocaleEnUS
		}
	}
	return LocaleZhCN
}

// SetLanguageResponseHeaders declares the selected response language and cache variance.
func SetLanguageResponseHeaders(w http.ResponseWriter, locale string) {
	w.Header().Set("Content-Language", NormalizeLocale(locale))
	for _, value := range w.Header().Values("Vary") {
		for _, item := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(item), "Accept-Language") {
				return
			}
		}
	}
	w.Header().Add("Vary", "Accept-Language")
}
