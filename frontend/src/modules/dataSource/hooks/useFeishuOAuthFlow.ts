import { useRef, useState } from "react";
import type { Dispatch, SetStateAction } from "react";
import { message } from "antd";
import type { TFunction } from "i18next";
import {
  FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
  consumeFeishuDataSourceOAuthResult,
  finishFeishuDataSourceOAuth,
  openCenteredPopup,
  requestFeishuDataSourceAuthorizeUrl,
  type FeishuDataSourceOAuthMessage,
} from "../common/feishuOAuth";
import {
  getOAuthStateFromConnection,
  type FeishuAuthAccount,
} from "../common/feishuAccounts";
import { FEISHU_DEFAULT_SCOPES } from "../constants/options";
import type { OAuthState, PendingOAuthAttempt } from "../constants/types";
import { getScanTenantId } from "../utils/scanAccessors";
import { parseFeishuOAuthCallbackInput } from "../utils/feishuAccount";

interface UseFeishuOAuthFlowParams {
  t: TFunction;
  setAccounts: Dispatch<SetStateAction<FeishuAuthAccount[]>>;
  refreshAccounts: () => Promise<void> | void;
}

// Encapsulates the feishu OAuth authorization flow: the pending attempt ref,
// popup lifecycle, result application onto the accounts list, and the manual
// callback fallback modal state.
export function useFeishuOAuthFlow({
  t,
  setAccounts,
  refreshAccounts,
}: UseFeishuOAuthFlowParams) {
  const [manualOauthModalOpen, setManualOauthModalOpen] = useState(false);
  const [manualOauthCallbackValue, setManualOauthCallbackValue] = useState("");
  const [manualOauthSubmitting, setManualOauthSubmitting] = useState(false);
  const oauthAttemptRef = useRef<PendingOAuthAttempt | null>(null);

  const clearOauthAttempt = () => {
    if (oauthAttemptRef.current?.timerId) {
      window.clearInterval(oauthAttemptRef.current.timerId);
    }
    oauthAttemptRef.current = null;
  };

  const restorePreviousOauthState = (
    messageText?: string,
    level: "warning" | "error" = "warning",
  ) => {
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
    setAccounts((current) =>
      current.map((item) =>
        item.id === attempt.accountId
          ? {
              ...item,
              status: attempt.previousState,
              connection: attempt.previousConnection,
              updatedAt: new Date().toISOString(),
            }
          : item,
      ),
    );
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
      setAccounts((current) => {
        const matchedAccount =
          current.find(
            (item) =>
              item.connection?.connectionId === payload.connection.connectionId ||
              (attempt?.accountId && item.id === attempt.accountId) ||
              item.appId === attempt?.appId,
          ) ||
          current.find((item) => item.status === "waiting") ||
          current[0];

        if (!matchedAccount) {
          return current;
        }

        return current.map((item) =>
          item.id === matchedAccount.id
            ? {
                ...item,
                status: nextOauthState,
                connection: payload.connection,
                updatedAt: new Date().toISOString(),
                lastAuthorizedAt: new Date().toISOString(),
              }
            : item,
        );
      });
      message.success(t("admin.dataSourceOauthSuccess"));
      window.setTimeout(() => {
        void refreshAccounts();
      }, 0);
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
    setAccounts((current) => {
      const matchedAccount =
        current.find((item) => item.id === attempt?.accountId) ||
        current.find((item) => item.status === "waiting");
      if (!matchedAccount) {
        return current;
      }

      return current.map((item) =>
        item.id === matchedAccount.id
          ? {
              ...item,
              status: "error" as OAuthState,
              connection: null,
              updatedAt: new Date().toISOString(),
            }
          : item,
      );
    });
    message.error(payload.message || t("admin.dataSourceOauthFailedRetry"));
  };

  const startFeishuOAuth = async (
    account: FeishuAuthAccount,
    options?: { reauthorizeConnectionId?: string },
  ) => {
    const previousState = account.status;
    const previousConnection = account.connection;
    const reauthorizeConnectionId = options?.reauthorizeConnectionId?.trim();

    try {
      setAccounts((current) =>
        current.map((item) =>
          item.id === account.id
            ? {
                ...item,
                status: "waiting" as OAuthState,
                updatedAt: new Date().toISOString(),
              }
            : item,
        ),
      );

      const authorizeUrl = reauthorizeConnectionId
        ? await requestFeishuDataSourceAuthorizeUrl({
            tenantId: getScanTenantId(),
            scopes: FEISHU_DEFAULT_SCOPES,
            returnUrl: window.location.href,
            reauthorizeConnectionId,
          })
        : await requestFeishuDataSourceAuthorizeUrl({
            tenantId: getScanTenantId(),
            appId: account.appId,
            appSecret: account.appSecret,
            scopes: FEISHU_DEFAULT_SCOPES,
            returnUrl: window.location.href,
          });

      oauthAttemptRef.current = {
        timerId: null,
        previousState,
        previousVerified: previousState === "connected",
        previousConnection,
        resolved: false,
        accountId: account.id,
        appId: account.appId,
      };

      if (reauthorizeConnectionId) {
        window.location.assign(authorizeUrl);
        return true;
      }

      const popup = openCenteredPopup(
        authorizeUrl,
        t("admin.dataSourceFeishuAuthWindowTitle"),
      );

      if (popup) {
        const timerId = window.setInterval(() => {
          if (!popup.closed) {
            return;
          }

          if (oauthAttemptRef.current?.resolved) {
            clearOauthAttempt();
            return;
          }

          // Fallback: postMessage may not have been processed yet — check
          // sessionStorage for the OAuth result saved by the callback page.
          const storedFallback = consumeFeishuDataSourceOAuthResult();
          if (storedFallback) {
            applyOauthResult(storedFallback);
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
      restorePreviousOauthState(
        error?.message || t("admin.dataSourceAuthorizeUrlFailed"),
        "error",
      );
      return false;
    }
  };

  const handleSubmitManualOauthCallback = async () => {
    const parsed = parseFeishuOAuthCallbackInput(manualOauthCallbackValue);
    if (!parsed) {
      message.warning(t("admin.dataSourceOauthManualCallbackInvalid"));
      return;
    }

    try {
      setManualOauthSubmitting(true);
      const connection = await finishFeishuDataSourceOAuth(parsed.code, parsed.state);
      applyOauthResult({
        channel: FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
        source: "feishu-data-source",
        status: "success",
        connection,
      });
      setManualOauthModalOpen(false);
      setManualOauthCallbackValue("");
    } catch (error: any) {
      applyOauthResult({
        channel: FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
        source: "feishu-data-source",
        status: "error",
        message: error?.message || t("admin.dataSourceOauthFailedRetry"),
      });
    } finally {
      setManualOauthSubmitting(false);
    }
  };

  return {
    oauthAttemptRef,
    clearOauthAttempt,
    applyOauthResult,
    startFeishuOAuth,
    manualOauthModalOpen,
    setManualOauthModalOpen,
    manualOauthCallbackValue,
    setManualOauthCallbackValue,
    manualOauthSubmitting,
    handleSubmitManualOauthCallback,
  };
}
