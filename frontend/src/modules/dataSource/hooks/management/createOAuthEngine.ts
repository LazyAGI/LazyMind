import { message } from "antd";
import { type CloudOAuthAppCredentialBody } from "@/api/generated/auth-client";
import { dataSourceCloudOauthApi } from "../../api/clients";
import {
  createFeishuAccountId,
  getOAuthStateFromConnection,
  loadFeishuAuthAccounts,
  persistFeishuAuthAccounts,
  type FeishuAccountFormValues,
  type FeishuAuthAccount,
} from "../../common/feishuAccounts";
import {
  FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
  clearFeishuDataSourceWizardDraft,
  consumeCloudDataSourceOAuthResult,
  consumeFeishuDataSourceOAuthResult,
  enableCloudConnectionForChat,
  requestCloudDataSourceAuthorizeUrl,
  openCenteredPopup,
  requestFeishuDataSourceAuthorizeUrl,
  saveFeishuDataSourceWizardDraft,
  type CloudDataSourceProvider,
  type FeishuDataSourceOAuthMessage,
  type FeishuDataSourceWizardDraft,
} from "@/modules/dataSource/common/feishuOAuth";
import { FEISHU_DEFAULT_SCOPES } from "../../constants/options";
import type { FeishuAppSetup, OAuthState } from "../../constants/types";
import { getScanTenantId } from "../../utils/scanAccessors";
import { pickScanAgent } from "../../utils/cloudSync";
import {
  getCloudConnectionItems,
  mapCloudConnectionToDataSourceConnection,
  mapCloudConnectionToFeishuAccount,
} from "../../mappers/dataSourceConnection";
import type { ManagementContext, StartCloudOAuthOptions } from "./context";

export function createOAuthEngine(ctx: ManagementContext) {
  const {
    t,
    form,
    oauthAttemptRef,
    setOauthState,
    setConnectionVerified,
    setOauthConnection,
    setNotionOauthConnection,
    setFeishuAuthAccounts,
    setWizardStep,
    setValidatedAgentId,
    feishuAuthAccountsLoadedRef,
    scanAgents,
  } = ctx;

  const refreshFeishuAuthAccounts = async () => {
    try {
      const response =
        await dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet({
          provider: "feishu",
          status: null,
        });
      const cachedAccounts = loadFeishuAuthAccounts();
      const nextAccounts = getCloudConnectionItems(response.data).map((item) =>
        mapCloudConnectionToFeishuAccount(item, cachedAccounts),
      );
      feishuAuthAccountsLoadedRef.current = true;
      setFeishuAuthAccounts(nextAccounts);
      persistFeishuAuthAccounts(nextAccounts);
      const connectedAccount = nextAccounts.find(
        (account) =>
          account.status === "connected" && Boolean(account.connection?.connectionId),
      );
      if (connectedAccount?.connection) {
        setOauthConnection(connectedAccount.connection);
        setOauthState("connected");
        setConnectionVerified(true);
      }
    } catch (error) {
      console.error("Failed to refresh Feishu auth accounts", error);
    }
  };

  const refreshNotionAuthConnection = async () => {
    try {
      const response =
        await dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet({
          provider: "notion",
          status: null,
        });
      const nextConnection = getCloudConnectionItems(response.data)
        .map((item) => mapCloudConnectionToDataSourceConnection(item, "notion"))
        .find(
          (connection) =>
            connection.status === "connected" && Boolean(connection.connectionId),
        ) || null;
      setNotionOauthConnection(nextConnection);
      if (nextConnection && ctx.selectedType === "notion") {
        setOauthConnection(nextConnection);
        setOauthState("connected");
        setConnectionVerified(true);
      }
    } catch (error) {
      console.error("Failed to refresh Notion auth connection", error);
    }
  };

  const clearOauthAttempt = () => {
    if (oauthAttemptRef.current?.timerId) {
      window.clearInterval(oauthAttemptRef.current.timerId);
    }
    oauthAttemptRef.current = null;
  };

  const restorePreviousOauthState = (messageText?: string, level: "warning" | "error" = "warning") => {
    const attempt = oauthAttemptRef.current;
    if (!attempt) {
      if (messageText) {
        message[level](messageText);
      }
      return;
    }

    if (attempt.timerId) {
      window.clearInterval(attempt.timerId);
    }
    setOauthState(attempt.previousState);
    setConnectionVerified(attempt.previousVerified);
    setOauthConnection(attempt.previousConnection);
    if (attempt.accountId) {
      setFeishuAuthAccounts((current) => {
        const nextAccounts = current.map((item) =>
          item.id === attempt.accountId
            ? {
                ...item,
                status: attempt.previousState,
                connection: attempt.previousConnection,
                updatedAt: new Date().toISOString(),
              }
            : item,
        );
        persistFeishuAuthAccounts(nextAccounts);
        return nextAccounts;
      });
    }
    oauthAttemptRef.current = null;

    if (messageText) {
      message[level](messageText);
    }
  };

  const applyOauthResult = (payload: FeishuDataSourceOAuthMessage) => {
    const attempt = oauthAttemptRef.current;

    if (payload.channel !== FEISHU_DATA_SOURCE_OAUTH_CHANNEL) {
      return;
    }

    if (attempt?.timerId) {
      window.clearInterval(attempt.timerId);
    }
    if (attempt) {
      attempt.resolved = true;
    }

    if (payload.status === "success") {
      oauthAttemptRef.current = null;
      const nextOauthState = getOAuthStateFromConnection(payload.connection);
      setOauthConnection(payload.connection);
      setOauthState(nextOauthState);
      setConnectionVerified(nextOauthState === "connected");
      if (payload.connection.provider === "notion") {
        setNotionOauthConnection(payload.connection);
        if (nextOauthState === "connected") {
          void enableCloudConnectionForChat(payload.connection.connectionId).catch((error) => {
            console.error("Failed to enable Notion connection for chat", error);
          });
        }
      }
      if (nextOauthState === "connected") {
        setFeishuAuthAccounts((current) => {
          if (payload.connection.provider !== "feishu") {
            return current;
          }
          const matchedAccount = current.find(
            (item) =>
              (attempt?.accountId && item.id === attempt.accountId) ||
              item.appId === attempt?.appId ||
              item.appId === ctx.feishuAppSetup?.appId,
          );
          if (!matchedAccount) {
            return current;
          }

          const nextAccounts = current.map((item) =>
            item.id === matchedAccount.id
              ? {
                  ...item,
                  name:
                    item.name ||
                    payload.connection.accountName ||
                    item.appId,
                  status: nextOauthState,
                  connection: payload.connection,
                  updatedAt: new Date().toISOString(),
                  lastAuthorizedAt: new Date().toISOString(),
                }
              : item,
          );
          persistFeishuAuthAccounts(nextAccounts);
          return nextAccounts;
        });
      }
      setWizardStep(1);
      message.success(t("admin.dataSourceOauthSuccess"));
      return;
    }

    if (attempt?.previousConnection) {
      restorePreviousOauthState(
        t("admin.dataSourceOauthReconnectFailed", {
          message: payload.message ? ` ${payload.message}` : "",
        }),
        "error",
      );
      return;
    }

    oauthAttemptRef.current = null;
    setOauthConnection(null);
    setOauthState("error");
    setConnectionVerified(false);
    if (attempt?.accountId) {
      setFeishuAuthAccounts((current) => {
        const nextAccounts = current.map((item) =>
          item.id === attempt.accountId
            ? {
                ...item,
                status: "error" as OAuthState,
                connection: null,
                updatedAt: new Date().toISOString(),
              }
            : item,
        );
        persistFeishuAuthAccounts(nextAccounts);
        return nextAccounts;
      });
    }
    message.error(payload.message || t("admin.dataSourceOauthFailedRetry"));
  };

  const upsertFeishuAuthAccount = (
    setup: FeishuAccountFormValues,
    status: OAuthState = "pending",
  ) => {
    const now = new Date().toISOString();
    const appId = setup.appId.trim();
    const appSecret = setup.appSecret.trim();
    const { editingFeishuAccountId, feishuAuthAccounts } = ctx;
    const existingAccount = editingFeishuAccountId
      ? feishuAuthAccounts.find((item) => item.id === editingFeishuAccountId)
      : feishuAuthAccounts.find((item) => item.appId === appId);
    const nextAccount: FeishuAuthAccount = {
      id: existingAccount?.id || createFeishuAccountId(),
      name: `${setup.name || ""}`.trim() || existingAccount?.name || appId,
      appId,
      appSecret,
      chatEnabled: existingAccount?.chatEnabled ?? false,
      status,
      connection: status === "pending" ? null : existingAccount?.connection || null,
      createdAt: existingAccount?.createdAt || now,
      updatedAt: now,
      lastAuthorizedAt:
        status === "connected" ? now : existingAccount?.lastAuthorizedAt,
    };
    const nextAccounts = existingAccount
      ? feishuAuthAccounts.map((item) =>
          item.id === existingAccount.id ? nextAccount : item,
        )
      : [nextAccount, ...feishuAuthAccounts];

    setFeishuAuthAccounts(nextAccounts);
    persistFeishuAuthAccounts(nextAccounts);
    return nextAccount;
  };

  const saveCloudAppCredentials = async (
    provider: CloudDataSourceProvider,
    setup: FeishuAppSetup,
  ) => {
    const body: CloudOAuthAppCredentialBody = {
      client_id: setup.appId,
      client_secret: setup.appSecret,
    };
    await dataSourceCloudOauthApi.saveOauthAppCredentialsApiAuthserviceV1CloudProviderOauthAppCredentialsPut({
      provider,
      cloudOAuthAppCredentialBody: body,
    });
  };

  const startCloudOAuth = async (
    provider: CloudDataSourceProvider,
    options?: StartCloudOAuthOptions,
  ) => {
    const activeSetup =
      options?.setup || (provider === "feishu" ? ctx.feishuAppSetup : ctx.notionAppSetup);
    const previousState = options?.previousState ?? ctx.oauthState;
    const previousVerified = options?.previousVerified ?? ctx.connectionVerified;
    const previousConnection = options?.previousConnection ?? ctx.oauthConnection;

    try {
      if (!activeSetup?.appId.trim() || !activeSetup.appSecret.trim()) {
        message.warning(
          provider === "feishu"
            ? t("admin.dataSourceFeishuCredentialRequired")
            : t("admin.dataSourceNotionCredentialRequired"),
        );
        return false;
      }

      const selectedAgent = pickScanAgent(scanAgents, ctx.validatedAgentId || undefined) || {
        agent_id: ctx.validatedAgentId || "",
        tenant_id: getScanTenantId(),
      };

      setOauthState("waiting");
      setValidatedAgentId(selectedAgent.agent_id || null);
      const requestAuthorizeUrl =
        provider === "feishu"
          ? requestFeishuDataSourceAuthorizeUrl
          : (input: Parameters<typeof requestCloudDataSourceAuthorizeUrl>[1]) =>
              requestCloudDataSourceAuthorizeUrl(provider, input);
      const authorizeUrl = await requestAuthorizeUrl({
        tenantId: selectedAgent.tenant_id || getScanTenantId(),
        appId: activeSetup.appId,
        appSecret: activeSetup.appSecret,
        scopes: provider === "feishu" ? FEISHU_DEFAULT_SCOPES : [],
        returnUrl: window.location.href,
      });

      const draft: FeishuDataSourceWizardDraft = {
        wizardOpen: options?.draftWizardOpen ?? true,
        wizardStep: options?.draftWizardStep ?? ctx.wizardStep,
        wizardMode: options?.draftWizardMode ?? ctx.wizardMode,
        selectedType: options?.draftSelectedType ?? ctx.selectedType,
        editingId: options?.draftEditingId ?? ctx.editingId,
        validatedAgentId: selectedAgent.agent_id || null,
        oauthState: "waiting",
        connectionVerified: previousVerified,
        oauthConnection: previousConnection,
        formValues: options?.draftFormValues || form.getFieldsValue(true),
      };

      saveFeishuDataSourceWizardDraft(draft);

      const popup = openCenteredPopup(
        authorizeUrl,
        provider === "feishu" ? t("admin.dataSourceFeishuAuthWindowTitle") : t("admin.dataSourceNotionAuthWindowTitle"),
      );

      if (options?.draftWizardOpen === false) {
        clearFeishuDataSourceWizardDraft();
      }

      oauthAttemptRef.current = {
        timerId: null,
        previousState,
        previousVerified,
        previousConnection,
        resolved: false,
        accountId: options?.accountId,
        appId: options?.appId || activeSetup.appId,
      };

      if (popup) {
        const timerId = window.setInterval(() => {
          if (!popup.closed) {
            return;
          }

          if (oauthAttemptRef.current?.resolved) {
            clearOauthAttempt();
            return;
          }

          // Fallback: postMessage may not have been processed yet —
          // check sessionStorage for OAuth result saved synchronously by callback page.
          const storedResult = consumeFeishuDataSourceOAuthResult();
          if (storedResult) {
            applyOauthResult(storedResult);
            return;
          }
          const storedCloudResult = consumeCloudDataSourceOAuthResult(
            (options?.draftSelectedType as CloudDataSourceProvider) || "notion",
          );
          if (storedCloudResult) {
            applyOauthResult(storedCloudResult);
            return;
          }

          restorePreviousOauthState(t("admin.dataSourceOauthWindowClosed"));
        }, 400);

        oauthAttemptRef.current.timerId = timerId;
        popup.focus();
        return true;
      }

      window.location.assign(authorizeUrl);
      return true;
    } catch (error: any) {
      setOauthState(previousState);
      setConnectionVerified(previousVerified);
      setOauthConnection(previousConnection);
      message.error(error?.message || t("admin.dataSourceAuthorizeUrlFailed"));
      return false;
    }
  };

  return {
    clearOauthAttempt,
    restorePreviousOauthState,
    applyOauthResult,
    refreshFeishuAuthAccounts,
    refreshNotionAuthConnection,
    upsertFeishuAuthAccount,
    saveCloudAppCredentials,
    startCloudOAuth,
  };
}
