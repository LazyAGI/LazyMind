package common

import (
	"net/http"
	"strings"
)

func init() {
	for _, group := range []struct {
		code    int
		status  int
		message string
		sources []string
	}{
		{2005200, http.StatusConflict, "Translation configuration is unavailable or changed", []string{
			"selected translation source is unavailable; configure it in default models",
			"selected translation provider differs from the requested artifact provider",
			"translation provider group was removed or changed; create a new translation job",
			"translation source changed; retranslate the block with the currently selected source",
		}},
		{2005201, http.StatusBadRequest, "Translation provider credentials or endpoint are invalid", []string{
			"provider_type and provider must both be supplied for a retranslated block",
			"invalid translation endpoint for %s",
			"youdao application id and secret are required",
			"deepl api key is required",
			"caiyun token is required",
			"tokenhub api key is required",
		}},
		{2005202, http.StatusBadRequest, "The translation request is not supported", []string{
			"%s request exceeds %d bytes",
			"baidu translation exceeds 1000 characters per request",
			"unsupported bing web translation target",
			"unsupported deepl web translation target",
			"unsupported google web translation target",
			"unsupported sogou web translation target",
			"unsupported translation target",
			"unsupported transmart translation target",
		}},
		{2005203, http.StatusBadGateway, "Translation provider request failed", []string{
			"%s translation returned http %d",
			"alibaba cloud translation failed (%s)",
			"alibaba cloud translation returned no text",
			"baidu translation failed (%s)",
			"baidu translation returned no text",
			"bing translator redirected to an unexpected origin",
			"bing translator session",
			"bing translator session request failed",
			"bing web translation",
			"bing web translation request failed",
			"bing web translation returned an invalid response",
			"bing web translation service returned status %d",
			"caiyun translation request failed",
			"caiyun translation returned an invalid response",
			"deepl translation request failed",
			"deepl translation returned an invalid response",
			"deepl web translation",
			"deepl web translation request failed",
			"deepl web translation returned an invalid response",
			"google cloud translation failed (%d)",
			"google cloud translation returned no text",
			"google web translation",
			"google web translation request failed",
			"google web translation returned an invalid response",
			"microsoft translation failed (%s)",
			"microsoft translation returned no text",
			"niutrans translation failed (%s)",
			"niutrans translation returned no text",
			"sogou web translation",
			"sogou web translation changed target from %s to %s",
			"sogou web translation followed too many redirects",
			"sogou web translation redirected to an unexpected origin",
			"sogou web translation request failed",
			"sogou web translation returned a response from an unexpected origin",
			"sogou web translation returned an invalid response",
			"tokenhub translation request failed",
			"tokenhub translation returned an invalid response",
			"tokenhub translation was truncated",
			"translation request failed",
			"translation response is too large",
			"translation service returned http %d",
			"transmart translation",
			"transmart translation failed",
			"transmart translation request failed",
			"transmart translation returned an invalid response",
			"volcengine translation failed (%s)",
			"volcengine translation returned no text",
			"youdao translation failed",
			"youdao translation request failed",
		}},
		{2005204, http.StatusInternalServerError, "Translation service initialization failed", []string{
			"initialize free translation services failed",
			"resolve document translation provider snapshot",
		}},
		{2005205, http.StatusConflict, "The selected translation provider does not support documents", []string{
			"selected translation provider does not support documents",
		}},
	} {
		for _, source := range group.sources {
			if strings.Contains(source, "%") {
				registerAdditionalErrorPattern(source, group.message, group.status, group.code)
			} else {
				registerAdditionalErrorAlias(source, group.message, group.status, group.code)
			}
		}
	}
}
