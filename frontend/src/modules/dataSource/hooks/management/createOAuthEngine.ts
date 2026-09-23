import { Modal, message } from "antd";
import {
  getLocalizedErrorMessage,
  localizeErrorCode,
} from "@/components/request";
import type { RawAxiosRequestConfig } from "axios";
import type { ProviderConnectionSession } from "@/api/generated/core-client";
import { dataSourceCloudOauthApi, dataSourceProviderConnectionsApi } from "../../api/clients";
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
  mapCloudConnectionToFeishuAccount,
  mapCloudConnectionToNotionAccount,
} from "../../mappers/dataSourceConnection";
import type { ManagementContext, StartCloudOAuthOptions } from "./context";
import {
  closeManagedAuthorizationPopup,
  openFeishuCLIAuthorization,
  openManagedAuthorization,
  reserveManagedAuthorizationPopup,
  type ReservedManagedAuthorizationPopup,
} from "../../oauth/openManagedAuthorization";
import { buildLegacyOAuthCredentialBody } from "../../oauth/legacyOAuthCredentials";

const PROVIDER_AUTH_SESSION_MAX_POLL_ATTEMPTS = 600;

type FeishuAuthorizationOpenResult =
  | { ok: true; popup: ReservedManagedAuthorizationPopup }
  | { ok: false };
type Translate = (key: string) => string;

function requestFeishuAuthorizationPopup(
  url: string,
  translate: Translate,
): Promise<ReservedManagedAuthorizationPopup> {
  return new Promise((resolve) => {
    let settled = false;
    const finish = (popup: ReservedManagedAuthorizationPopup) => {
      if (!settled) {
        settled = true;
        resolve(popup);
      }
    };

    Modal.confirm({
      title: translate("modelProvider.cloudDocuments.feishuPopupBlockedTitle"),
      content: translate("modelProvider.cloudDocuments.feishuPopupBlockedDescription"),
      okText: translate("modelProvider.cloudDocuments.feishuPopupBlockedAction"),
      cancelText: translate("common.cancel"),
      onOk: async () => {
        const popup = reserveManagedAuthorizationPopup();
        if (!popup) {
          message.error(
            translate("modelProvider.cloudDocuments.feishuPopupStillBlocked"),
          );
          finish(null);
          return;
        }
        const opened = await openFeishuCLIAuthorization(url, popup);
        if (!opened.ok) {
          closeManagedAuthorizationPopup(popup);
          message.error(
            translate("modelProvider.cloudDocuments.feishuPopupStillBlocked"),
          );
          finish(null);
          return;
        }
        finish(popup);
      },
      onCancel: () => finish(null),
    });
  });
}

async function openFeishuAuthorizationWithRecovery(
  url: string,
  popup: ReservedManagedAuthorizationPopup,
  translate: Translate,
): Promise<FeishuAuthorizationOpenResult> {
  const opened = await openFeishuCLIAuthorization(url, popup);
  if (opened.ok) {
    return { ok: true, popup };
  }
  if (opened.reason !== "blocked") {
    return { ok: false };
  }
  const recoveredPopup = await requestFeishuAuthorizationPopup(url, translate);
  return recoveredPopup ? { ok: true, popup: recoveredPopup } : { ok: false };
}

export async function startFeishuCLISession(
  reauthorizeConnectionId?: string,
  onOpened?: () => void,
  translate: Translate = (key) => key,
): Promise<string | null> {
  let popup = reserveManagedAuthorizationPopup();
  const connectionId = reauthorizeConnectionId?.trim();
  let session: ProviderConnectionSession;
  try {
    const created = connectionId
      ? await dataSourceProviderConnectionsApi.apiCoreProviderConnectionsAuthConnectionIdReauthorizePost({ authConnectionId: connectionId })
      : await dataSourceProviderConnectionsApi.apiCoreProviderConnectionsSessionsPost({ providerConnectionCreateRequest: { provider: "feishu" } });
    session = created.data;
  } catch (error) {
    closeManagedAuthorizationPopup(popup);
    if (typeof error === "object" && error !== null && "response" in error) {
      return null;
    }
    throw error;
  }
  if (!session.session_id || !session.authorization_start_url) {
    closeManagedAuthorizationPopup(popup);
    return null;
  }
  let openedURL = session.authorization_start_url;
  let opened = await openFeishuAuthorizationWithRecovery(openedURL, popup, translate);
  if (!opened.ok) {
    return null;
  }
  popup = opened.popup;
  onOpened?.();
  for (let attempt = 0; attempt < PROVIDER_AUTH_SESSION_MAX_POLL_ATTEMPTS; attempt += 1) {
    await new Promise((resolve) => window.setTimeout(resolve, 1000));
    let status: ProviderConnectionSession;
    try {
      const response = await dataSourceProviderConnectionsApi.apiCoreProviderConnectionsSessionsSessionIdGet({ sessionId: session.session_id });
      status = response.data;
    } catch (error) {
      if (typeof error === "object" && error !== null && "response" in error) {
        continue;
      }
      throw error;
    }
    if (
      status.authorization_start_url &&
      status.authorization_start_url !== openedURL
    ) {
      openedURL = status.authorization_start_url;
      opened = await openFeishuAuthorizationWithRecovery(openedURL, popup, translate);
      if (!opened.ok) {
        return null;
      }
      popup = opened.popup;
    }
    if (status.status === "COMPLETED" && status.auth_connection_id) {
      closeManagedAuthorizationPopup(popup);
      return status.auth_connection_id;
    }
    if (
      [
        "APP_CREATION_DENIED",
        "APP_CREATION_FORBIDDEN",
        "AUTH_DEVICE_CODE_EXPIRED",
        "AUTH_CANCELED",
        "AUTH_SCOPE_MISSING",
        "AUTH_TOKEN_EXPIRED",
        "AUTH_REFRESH_FAILED",
        "PROFILE_NOT_FOUND",
        "PROFILE_OWNER_MISMATCH",
        "PROFILE_TENANT_MISMATCH",
        "CLI_NOT_INSTALLED",
        "CLI_VERSION_UNSUPPORTED",
        "CLI_INTEGRITY_MISMATCH",
        "CLI_OUTPUT_INVALID",
        "CLI_TIMEOUT",
        "CLI_UNAVAILABLE",
      ].includes(status.status || "")
    ) {
      closeManagedAuthorizationPopup(popup);
      return null;
    }
  }
  closeManagedAuthorizationPopup(popup);
  return null;
}

export async function startManagedOAuthSession(
  provider: CloudDataSourceProvider,
  reauthorizeConnectionId?: string,
  onOpened?: () => void,
): Promise<string | null> {
  const popup = reserveManagedAuthorizationPopup();
  if (popup === null) {
    return null;
  }
  const connectionId = reauthorizeConnectionId?.trim();
  let session: ProviderConnectionSession;
  try {
    const created = connectionId
      ? await dataSourceProviderConnectionsApi.apiCoreProviderConnectionsAuthConnectionIdReauthorizePost({ authConnectionId: connectionId })
      : await dataSourceProviderConnectionsApi.apiCoreProviderConnectionsSessionsPost({ providerConnectionCreateRequest: { provider } });
    session = created.data;
  } catch (error) {
    closeManagedAuthorizationPopup(popup);
    if (typeof error === "object" && error !== null && "response" in error) {
      return null;
    }
    throw error;
  }
  if (!session.session_id || !session.authorization_start_url) {
    closeManagedAuthorizationPopup(popup);
    return null;
  }
  const opened = await openManagedAuthorization(session.authorization_start_url, popup);
  if (!opened.ok) {
    return null;
  }
  onOpened?.();
  for (let attempt = 0; attempt < PROVIDER_AUTH_SESSION_MAX_POLL_ATTEMPTS; attempt += 1) {
    await new Promise((resolve) => window.setTimeout(resolve, 1000));
    let status: ProviderConnectionSession;
    try {
      const response = await dataSourceProviderConnectionsApi.apiCoreProviderConnectionsSessionsSessionIdGet({ sessionId: session.session_id });
      status = response.data;
    } catch (error) {
      if (typeof error === "object" && error !== null && "response" in error) {
        continue;
      }
      throw error;
    }
    if (status.status === "COMPLETED" && status.auth_connection_id) {
      return status.auth_connection_id;
    }
    if (["DENIED", "CANCELED", "EXPIRED", "ERROR"].includes(status.status || "")) {
      return null;
    }
  }
  return null;
}

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
      if (ctx.cloudManagedOAuthAvailable !== false) {
        try {
          const options: RawAxiosRequestConfig & { silentError: boolean } = { silentError: true };
          await dataSourceProviderConnectionsApi.apiCoreProviderConnectionsGet(options);
        } catch {
          // Cloud reconciliation is best effort; existing local accounts remain usable offline.
        }
      }
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
      chatEnabled: existingAccount?.chatEnabled ?? true,
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
    const body = buildLegacyOAuthCredentialBody(setup);
    await dataSourceCloudOauthApi.saveOauthAppCredentialsApiAuthserviceV1CloudProviderOauthAppCredentialsPut({
      provider,
      cloudOAuthAppCredentialBody: body,
    });
  };

  const startManagedOAuth = async (
    provider: CloudDataSourceProvider,
    reauthorizeConnectionId?: string,
  ) => {
    const connectionId = await startManagedOAuthSession(
      provider,
      reauthorizeConnectionId,
      () => setOauthState("waiting"),
    );
    if (!connectionId) {
      return false;
    }
    await enableCloudConnectionForChat(connectionId);
    if (provider === "notion") {
      await refreshNotionAuthAccounts();
    } else {
      await refreshFeishuAuthAccounts();
    }
    return true;
  };

  const startCloudOAuth = async (
    provider: CloudDataSourceProvider,
    options?: StartCloudOAuthOptions,
  ) => {
    if (!options?.setup) {
      if (provider === "feishu") {
        const connectionId = await startFeishuCLISession(
          options?.reauthorizeConnectionId,
          () => setOauthState("waiting"),
          t,
        );
        if (!connectionId) {
          return false;
        }
        await refreshFeishuAuthAccounts();
        return true;
      }
	  if (ctx.cloudManagedOAuthAvailable === false) {
		return false;
	  }
      return startManagedOAuth(provider, options?.reauthorizeConnectionId);
    }
    const activeSetup =
      options?.setup || (provider === "feishu" ? ctx.feishuAppSetup : ctx.notionAppSetup);
    const previousState = options?.previousState ?? ctx.oauthState;
    const previousVerified = options?.previousVerified ?? ctx.connectionVerified;
    const previousConnection = options?.previousConnection ?? ctx.oauthConnection;

    try {
      if (!activeSetup?.appId.trim()) {
        const credentialRequiredKey =
          provider === "feishu"
            ? "admin.dataSourceFeishuCredentialRequired"
            : provider === "github"
              ? "admin.dataSourceGithubCredentialRequired"
              : "admin.dataSourceNotionCredentialRequired";
        message.warning(t(credentialRequiredKey));
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
        options?.draftSelectedType === "feishu" ||
        options?.draftSelectedType === "notion"
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
        provider === "feishu"
          ? t("admin.dataSourceFeishuAuthWindowTitle")
          : provider === "github"
            ? t("admin.dataSourceGithubAuthWindowTitle")
            : t("admin.dataSourceNotionAuthWindowTitle"),
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
          const storedResult = consumeCloudDataSourceOAuthResult(
            oauthAttemptRef.current?.provider || "notion",
          );
          if (storedResult) {
            applyOauthResult(storedResult);
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
