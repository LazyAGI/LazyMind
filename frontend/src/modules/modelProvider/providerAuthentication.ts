import type { AddedProvider, ProviderConnectionGroup } from "./pages/ModelProvidersPage";

/** Discovery is allowed before verification only for keyless custom services and the local vLLM preset. */
export function canDiscoverWithoutApiKey(
  provider: Pick<AddedProvider, "isCustom" | "baseUrl"> & Partial<Pick<AddedProvider, "source" | "protocolName" | "name">>,
  group?: Pick<ProviderConnectionGroup, "baseUrl" | "apiKeyConfigured" | "keys">,
) {
  const localService = [provider.source, provider.protocolName, provider.name].some((name) => name?.trim().toLowerCase() === "vllm");
  if ((!provider.isCustom && !localService) || group?.apiKeyConfigured || group?.keys.length) return false;
  try {
    const endpoint = new URL(group?.baseUrl || provider.baseUrl);
    return /^https?:$/.test(endpoint.protocol) && Boolean(endpoint.hostname) &&
      !endpoint.username && !endpoint.password && !endpoint.search && !endpoint.hash;
  } catch {
    return false;
  }
}
