import { getProviderDisplayName } from "@/modules/modelProvider/providerDisplayNames";

interface ChatProviderIdentity {
  provider_name?: string;
  provider_display_name?: string;
  provider_is_custom?: boolean;
}

export function chatProviderDisplayName(identity: ChatProviderIdentity, language = "en-US"): string {
  const name = identity.provider_display_name?.trim();
  // Old messages have only a runtime adapter; do not infer a supplier from it.
  if (!name) return identity.provider_name || "";
  return getProviderDisplayName({ name, is_custom: identity.provider_is_custom }, language);
}
