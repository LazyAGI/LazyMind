import type { AddedProvider, ProviderConnectionGroup } from "./pages/ModelProvidersPage";

/** Discovery is allowed before verification only for genuinely keyless custom services. */
export function canDiscoverWithoutApiKey(
  provider: Pick<AddedProvider, "isCustom" | "baseUrl">,
  group?: Pick<ProviderConnectionGroup, "baseUrl" | "apiKeyConfigured" | "keys">,
) {
  if (!provider.isCustom || group?.apiKeyConfigured || group?.keys.length) return false;
  try {
    const endpoint = new URL(group?.baseUrl || provider.baseUrl);
    return /^https?:$/.test(endpoint.protocol) && Boolean(endpoint.hostname) &&
      !endpoint.username && !endpoint.password && !endpoint.search && !endpoint.hash;
  } catch {
    return false;
  }
}
