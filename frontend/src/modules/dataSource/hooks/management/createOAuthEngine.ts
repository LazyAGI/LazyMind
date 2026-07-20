import { message } from "antd";
import {
  getLocalizedErrorMessage,
  localizeErrorCode,
} from "@/components/request";
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
  peekFeishuDataSourceWizardDraft,
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
  mapCloudConnectionToNotionAccount,
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
    setNotionAuthAccounts,
    setFeishuAuthAccounts,
    setWizardStep,
    setValidatedAgentId,
    setAuthSelectModalOpen,
    setAuthSelectProvider,
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

  const refreshNotionAuthAccounts = async () => {
    try {
      const response =
        await dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet({
          provider: "notion",
          status: null,
        });
      const cachedAccounts = Array.isArray(ctx.notionAuthAccounts)
        ? ctx.notionAuthAccounts
        : [];
      const nextAccounts = getCloudConnectionItems(response.data).map((item) =>
        mapCloudConnectionToNotionAccount(item, cachedAccounts),
      );
      setNotionAuthAccounts(nextAccounts);
      const connectedAccount = nextAccounts.find(
        (account) =>
          account.status === "connected" && Boolean(account.connection?.connectionId),
      );
      const nextConnection = connectedAccount?.connection || null;
      setNotionOauthConnection(nextConnection);
      if (nextConnection && ctx.selectedType === "notion") {
        setOauthConnection(nextConnection);
        setOauthState("connected");
        setConnectionVerified(true);
      }
    } catch (error) {
      console.error("Failed to refresh Notion auth accounts", error);
    }
  };

  const refreshNotionAuthConnection = refreshNotionAuthAccounts;

  const clearOauthAttempt = () => {
    if (oauthAttemptRef.current?.timerId) {
      window.clearInterval(oauthAttemptRef.current.timerId);
    }
    oauthAttemptRef.current = null;
  };

  const restorePreviousOauthState = (messageText?: string, level: "warning" | "error" = "warning") => {
    const attempt = oauthAttemptRef.current;
    if (!attempt) {
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
    const shouldReopenSetup = attempt.reopenSetupOnFailure && attempt.provider;
    oauthAttemptRef.current = null;
    clearFeishuDataSourceWizardDraft();

    if (shouldReopenSetup) {
      ctx.openCloudSetupModal(attempt.provider!, "create");
    }

    if (messageText) {
      message[level](messageText);
    }
  };

  const applyOauthResult = (
    payload: FeishuDataSourceOAuthMessage,
    options?: { openWizardOnSuccess?: boolean },
  ) => {
    const attempt = oauthAttemptRef.current;
    const shouldOpenWizard =
      attempt?.openWizardOnSuccess || options?.openWizardOnSuccess;
    const shouldReopenSetupOnFailure =
      attempt?.reopenSetupOnFailure || options?.openWizardOnSuccess;
    const cloudProvider =
      attempt?.provider ||
      (payload.status === "error" ? payload.provider : undefined) ||
      (payload.status === "success" ? payload.connection.provider : undefined);

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
        setNotionAuthAccounts((current) => {
          const matchedAccount = current.find(
            (item) => item.connection?.connectionId === payload.connection.connectionId,
          );
          if (!matchedAccount) {
            return [
              {
                id: payload.connection.connectionId,
                name: payload.connection.accountName || payload.connection.connectionId,
                appId: attempt?.appId || ctx.notionAppSetup?.appId || "",
                appSecret: ctx.notionAppSetup?.appSecret || "",
                chatEnabled: false,
                status: nextOauthState,
                connection: payload.connection,
                createdAt: new Date().toISOString(),
                updatedAt: new Date().toISOString(),
                lastAuthorizedAt: new Date().toISOString(),
              },
              ...current,
            ];
          }
          return current.map((item) =>
            item.id === matchedAccount.id
              ? {
                  ...item,
                  name: payload.connection.accountName || item.name,
                  status: nextOauthState,
                  connection: payload.connection,
                  updatedAt: new Date().toISOString(),
                  lastAuthorizedAt: new Date().toISOString(),
                }
              : item,
          );
        });
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
      const pendingDraft = peekFeishuDataSourceWizardDraft();
      clearFeishuDataSourceWizardDraft();
      if (pendingDraft?.authSelectModalOpen) {
        const provider = pendingDraft.authSelectProvider || "feishu";
        const refreshAccounts =
          provider === "notion" ? refreshNotionAuthAccounts : refreshFeishuAuthAccounts;
        void refreshAccounts().then(() => {
          setAuthSelectProvider(provider);
          setAuthSelectModalOpen(true);
        });
      } else if (shouldOpenWizard) {
        ctx.setWizardOpen(true);
      }
      message.success(t("admin.dataSourceOauthSuccess"));
      return;
    }

    if (shouldReopenSetupOnFailure && cloudProvider) {
      oauthAttemptRef.current = null;
      clearFeishuDataSourceWizardDraft();
      setOauthConnection(null);
      setOauthState("error");
      setConnectionVerified(false);
      ctx.openCloudSetupModal(cloudProvider, "create");
      message.error(localizeErrorCode("2000509"));
      return;
    }

    if (attempt?.previousConnection) {
      restorePreviousOauthState(
        localizeErrorCode("2000509"),
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
    message.error(localizeErrorCode("2000509"));
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
      if (!activeSetup?.appId.trim()) {
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
      const draftFormValues =
        options?.draftFormValues ??
        (options?.draftWizardOpen === false ? {} : form.getFieldsValue(true));

      const existingDraft = peekFeishuDataSourceWizardDraft();
      const draftSelectedType =
        options?.draftSelectedType === "feishu" || options?.draftSelectedType === "notion"
          ? options.draftSelectedType
          : ctx.selectedType;
      const draft: FeishuDataSourceWizardDraft = {
        wizardOpen: false,
        openWizardAfterOAuth: options?.openWizardOnSuccess,
        authSelectModalOpen: existingDraft?.authSelectModalOpen,
        authSelectProvider: existingDraft?.authSelectProvider,
        wizardStep: options?.draftWizardStep ?? ctx.wizardStep,
        wizardMode: options?.draftWizardMode ?? ctx.wizardMode,
        selectedType: draftSelectedType,
        editingId: options?.draftEditingId ?? ctx.editingId,
        validatedAgentId: selectedAgent.agent_id || null,
        oauthState: "waiting",
        connectionVerified: previousVerified,
        oauthConnection: previousConnection,
        formValues: draftFormValues,
      };

      saveFeishuDataSourceWizardDraft(draft);

      const popup = openCenteredPopup(
        authorizeUrl,
        provider === "feishu" ? t("admin.dataSourceFeishuAuthWindowTitle") : t("admin.dataSourceNotionAuthWindowTitle"),
      );

      oauthAttemptRef.current = {
        timerId: null,
        previousState,
        previousVerified,
        previousConnection,
        resolved: false,
        accountId: options?.accountId,
        appId: options?.appId || activeSetup.appId,
        provider,
        openWizardOnSuccess: options?.openWizardOnSuccess,
        reopenSetupOnFailure: options?.reopenSetupOnFailure,
      };

      if (popup) {
        const timerId = window.setInterval(() => {
          if (!popup.closed) {
            return;
          }

          window.clearInterval(timerId);

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

        if (oauthAttemptRef.current) {
          oauthAttemptRef.current.timerId = timerId;
        }
        popup.focus();
        return true;
      }

      window.location.assign(authorizeUrl);
      return true;
    } catch (error) {
      setOauthState(previousState);
      setConnectionVerified(previousVerified);
      setOauthConnection(previousConnection);
      const requestError = error as { response?: unknown; request?: unknown };
      if (!requestError?.response && !requestError?.request) {
        message.error(
          getLocalizedErrorMessage(error),
        );
      }
      return false;
    }
  };

  return {
    clearOauthAttempt,
    restorePreviousOauthState,
    applyOauthResult,
    refreshFeishuAuthAccounts,
    refreshNotionAuthConnection,
    refreshNotionAuthAccounts,
    upsertFeishuAuthAccount,
    saveCloudAppCredentials,
    startCloudOAuth,
  };
}
