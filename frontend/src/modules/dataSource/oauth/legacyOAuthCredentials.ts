import type { CloudOAuthAppCredentialBody } from "@/api/generated/auth-client";
import type { FeishuAppSetup } from "../constants/types";

export function buildLegacyOAuthCredentialBody(
  setup: FeishuAppSetup,
): CloudOAuthAppCredentialBody {
  return {
    client_id: setup.appId,
    client_secret: setup.appSecret,
  };
}
