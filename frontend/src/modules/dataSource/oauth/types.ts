export const FEISHU_DATA_SOURCE_OAUTH_CHANNEL =
  "lazymind:datasource:feishu-oauth";
export const CLOUD_DATA_SOURCE_OAUTH_CHANNEL = FEISHU_DATA_SOURCE_OAUTH_CHANNEL;

export type CloudDataSourceProvider = "feishu" | "notion";

export type FeishuConnectionStatus =
  | "pending"
  | "connected"
  | "expired"
  | "error";

export interface FeishuDataSourceConnection {
  provider: CloudDataSourceProvider;
  connectionId: string;
  status: FeishuConnectionStatus;
  accountName: string;
  grantedScopes: string[];
  connectedAt?: string;
  expiresAt?: string;
  refreshExpiresAt?: string;
  tenantKey?: string;
  openId?: string;
  unionId?: string;
  avatarUrl?: string;
  accessTokenMasked?: string;
  refreshTokenMasked?: string;
}

export interface FeishuDataSourceWizardDraft {
  activeView?: "assets" | "connectors";
  authSelectModalOpen?: boolean;
  wizardOpen: boolean;
  wizardStep: number;
  wizardMode: "create" | "edit";
  selectedType: string | null;
  editingId: string | null;
  validatedAgentId?: string | null;
  oauthState: string;
  connectionVerified: boolean;
  oauthConnection: FeishuDataSourceConnection | null;
  formValues: Record<string, unknown>;
}

export interface FeishuPendingOAuthSession {
  provider?: CloudDataSourceProvider;
  tenantId: string;
  connectionId: string;
  state: string;
  redirectUri: string;
  returnUrl: string;
}

export type FeishuDataSourceOAuthMessage =
  | {
      channel: typeof FEISHU_DATA_SOURCE_OAUTH_CHANNEL;
      source: "feishu-data-source" | "notion-data-source";
      status: "success";
      connection: FeishuDataSourceConnection;
    }
  | {
      channel: typeof FEISHU_DATA_SOURCE_OAUTH_CHANNEL;
      source: "feishu-data-source" | "notion-data-source";
      status: "error";
      message: string;
      provider?: CloudDataSourceProvider;
    };

export type CloudDataSourceOAuthMessage = FeishuDataSourceOAuthMessage;

export type FeishuAuthorizeUrlInput =
  | {
      tenantId: string;
      appId: string;
      appSecret: string;
      scopes: string[];
      returnUrl?: string;
      reauthorizeConnectionId?: never;
    }
  | {
      tenantId: string;
      scopes: string[];
      returnUrl?: string;
      appId?: never;
      appSecret?: never;
      reauthorizeConnectionId: string;
    };
