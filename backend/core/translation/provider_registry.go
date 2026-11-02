package translation

import "lazymind/core/modelprovider"

type providerSpec = modelprovider.TranslationProviderSpec

func lookupProvider(name string) (providerSpec, bool) {
	spec, ok := modelprovider.TranslationProvider(name)
	return spec, ok && spec.Available
}
