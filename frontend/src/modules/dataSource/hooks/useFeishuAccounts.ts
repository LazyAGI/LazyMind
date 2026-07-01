import { useCallback, useEffect, useState } from "react";
import { Form, Modal, message } from "antd";
import { useTranslation } from "react-i18next";
import type { CloudConnectionUpdateBody } from "@/api/generated/auth-client";
import { getLocalizedErrorMessage } from "@/components/request";
import { dataSourceCloudOauthApi } from "../api/clients";
import {
  FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
  consumeFeishuDataSourceOAuthResult,
  getFeishuDataSourceCallbackUrl,
  type FeishuDataSourceOAuthMessage,
} from "../common/feishuOAuth";
import {
  createFeishuAccountId,
  type FeishuAccountFormValues,
  type FeishuAuthAccount,
} from "../common/feishuAccounts";
import {
  getCloudConnectionItems,
  mapCloudConnectionToFeishuAccount,
} from "../mappers/cloudConnection";
import { isFeishuAccountAuthValid } from "../utils/feishuAccount";
import { useFeishuOAuthFlow } from "./useFeishuOAuthFlow";

export function useFeishuAccounts() {
  const { t } = useTranslation();
  const [form] = Form.useForm<FeishuAccountFormValues>();
  const callbackUrl = getFeishuDataSourceCallbackUrl();
  const [accounts, setAccounts] = useState<FeishuAuthAccount[]>([]);
  const [accountsLoading, setAccountsLoading] = useState(false);
  const [modalOpen, setModalOpen] = useState(false);
  const [editingAccountId, setEditingAccountId] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const persistAccounts = (nextAccounts: FeishuAuthAccount[]) => {
    setAccounts(nextAccounts);
  };

  const refreshAccounts = useCallback(async () => {
    setAccountsLoading(true);
    try {
      const response =
        await dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet({
          provider: "feishu",
          status: null,
        });
      setAccounts((currentAccounts) =>
        getCloudConnectionItems(response.data).map((item) =>
          mapCloudConnectionToFeishuAccount(item, currentAccounts),
        ),
      );
    } catch (error) {
      message.error(
        getLocalizedErrorMessage(error, t("common.requestFailed")) ||
          t("common.requestFailed"),
      );
    } finally {
      setAccountsLoading(false);
    }
  }, [t]);

  const oauth = useFeishuOAuthFlow({ t, setAccounts, refreshAccounts });
  const { applyOauthResult, startFeishuOAuth, clearOauthAttempt } = oauth;

  useEffect(() => {
    void refreshAccounts();

    const storedResult = consumeFeishuDataSourceOAuthResult();
    if (storedResult) {
      window.setTimeout(() => {
        applyOauthResult(storedResult);
      }, 0);
    }

    const handleMessage = (event: MessageEvent<FeishuDataSourceOAuthMessage>) => {
      if (event.origin !== window.location.origin) {
        return;
      }
      if (!event.data || event.data.channel !== FEISHU_DATA_SOURCE_OAUTH_CHANNEL) {
        return;
      }
      applyOauthResult(event.data);
    };

    window.addEventListener("message", handleMessage);

    return () => {
      window.removeEventListener("message", handleMessage);
      clearOauthAttempt();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshAccounts]);

  const openAccountModal = (account?: FeishuAuthAccount) => {
    setEditingAccountId(account?.id || null);
    form.setFieldsValue({
      name: account?.name || "",
      appId: account?.appId || "",
      appSecret: account?.appSecret || "",
    });
    setModalOpen(true);
  };

  const updateFeishuConnection = async (
    connectionId: string,
    body: CloudConnectionUpdateBody,
  ) => {
    await dataSourceCloudOauthApi.updateConnectionApiAuthserviceV1CloudConnectionsConnectionIdPut(
      {
        connectionId,
        cloudConnectionUpdateBody: body,
      },
    );
  };

  const upsertAccount = (values: FeishuAccountFormValues) => {
    const now = new Date().toISOString();
    const appId = values.appId.trim();
    const appSecret = values.appSecret.trim();
    const existingAccount = editingAccountId
      ? accounts.find((item) => item.id === editingAccountId)
      : accounts.find((item) => item.appId === appId);
    const nextAccount: FeishuAuthAccount = {
      id: existingAccount?.id || createFeishuAccountId(),
      name: `${values.name || ""}`.trim() || existingAccount?.name || appId,
      appId,
      appSecret,
      chatEnabled: existingAccount?.chatEnabled ?? false,
      status: "pending",
      connection: null,
      createdAt: existingAccount?.createdAt || now,
      updatedAt: now,
      lastAuthorizedAt: existingAccount?.lastAuthorizedAt,
    };
    const nextAccounts = existingAccount
      ? accounts.map((item) => (item.id === existingAccount.id ? nextAccount : item))
      : [nextAccount, ...accounts];

    persistAccounts(nextAccounts);
    return nextAccount;
  };

  const handleSaveAccount = async () => {
    if (submitting) {
      return;
    }

    setSubmitting(true);
    try {
      const values = await form.validateFields();
      const existingAccount = editingAccountId
        ? accounts.find((item) => item.id === editingAccountId)
        : null;
      const connectionId = existingAccount?.connection?.connectionId?.trim();

      if (existingAccount && connectionId) {
        const appId = values.appId.trim();
        const appSecret = values.appSecret.trim();
        const displayName = `${values.name || ""}`.trim() || existingAccount.name || appId;
        const updateBody: CloudConnectionUpdateBody = {
          display_name: displayName,
          name: displayName,
          client_id: appId,
          app_id: appId,
          client_secret: appSecret,
          app_secret: appSecret,
          provider_account_meta: {
            ...(existingAccount.connection
              ? {
                  account_name: existingAccount.connection.accountName,
                  open_id: existingAccount.connection.openId,
                  tenant_key: existingAccount.connection.tenantKey,
                }
              : {}),
            client_id: appId,
            app_id: appId,
            name: displayName,
            display_name: displayName,
          },
        };

        await updateFeishuConnection(connectionId, updateBody);

        const now = new Date().toISOString();
        const updatedAccount: FeishuAuthAccount = {
          ...existingAccount,
          name: displayName,
          appId,
          appSecret,
          updatedAt: now,
          connection: existingAccount.connection
            ? {
                ...existingAccount.connection,
                accountName: displayName,
              }
            : existingAccount.connection,
        };
        const nextAccounts = accounts.map((item) =>
          item.id === existingAccount.id ? updatedAccount : item,
        );
        persistAccounts(nextAccounts);
        setModalOpen(false);
        setEditingAccountId(null);
        message.success(t("admin.dataSourceFeishuCredentialSaved"));
        await startFeishuOAuth(updatedAccount, {
          reauthorizeConnectionId: connectionId,
        });
        return;
      }

      const account = upsertAccount(values);
      setModalOpen(false);
      setEditingAccountId(null);
      message.success(t("admin.dataSourceFeishuCredentialSaved"));
      await startFeishuOAuth(account);
    } finally {
      setSubmitting(false);
    }
  };

  const handleAuthorizeAccount = (account: FeishuAuthAccount) => {
    const connectionId = account.connection?.connectionId?.trim();

    if (connectionId) {
      void startFeishuOAuth(account, {
        reauthorizeConnectionId: connectionId,
      });
      return;
    }

    if (!account.appId || !account.appSecret) {
      openAccountModal(account);
      message.warning(t("admin.dataSourceFeishuCredentialFirst"));
      return;
    }

    void startFeishuOAuth(account);
  };

  const handleDeleteAccount = (account: FeishuAuthAccount) => {
    Modal.confirm({
      title: t("admin.dataSourceFeishuAccountDeleteTitle"),
      content: t("admin.dataSourceFeishuAccountDeleteContent", {
        name: account.name,
      }),
      okText: t("common.confirm"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      onOk: async () => {
        const connectionId = account.connection?.connectionId?.trim();
        if (connectionId) {
          try {
            await dataSourceCloudOauthApi.deleteConnectionApiAuthserviceV1CloudConnectionsConnectionIdDelete(
              {
                connectionId,
              },
            );
          } catch (error: any) {
            message.error(
              getLocalizedErrorMessage(error, t("admin.dataSourceDeleteFailed")) ||
                t("admin.dataSourceDeleteFailed"),
            );
            throw error;
          }
        }

        persistAccounts(accounts.filter((item) => item.id !== account.id));
        if (connectionId) {
          await refreshAccounts();
        }
      },
    });
  };

  const handleToggleChat = (account: FeishuAuthAccount, checked: boolean) => {
    if (checked && !isFeishuAccountAuthValid(account)) {
      message.warning(t("admin.dataSourceFeishuAccountChatAuthRequired"));
      return;
    }

    const connectionId = account.connection?.connectionId?.trim();
    const previousAccounts = accounts;

    setAccounts((current) =>
      current.map((item) =>
        item.id === account.id
          ? { ...item, chatEnabled: checked, updatedAt: new Date().toISOString() }
          : item,
      ),
    );

    if (!connectionId) {
      return;
    }

    updateFeishuConnection(connectionId, {
      chat_enabled: checked,
      chatEnabled: checked,
    })
      .then(() => {
        message.success(
          checked
            ? t("admin.dataSourceFeishuAccountChatEnabledSuccess", {
                name: account.name,
              })
            : t("admin.dataSourceFeishuAccountChatDisabledSuccess", {
                name: account.name,
              }),
        );
      })
      .catch((error: any) => {
        persistAccounts(previousAccounts);
        message.error(
          getLocalizedErrorMessage(error, t("common.requestFailed")) ||
            t("common.requestFailed"),
        );
      });
  };

  return {
    t,
    form,
    callbackUrl,
    accounts,
    accountsLoading,
    modalOpen,
    editingAccountId,
    submitting,
    manualOauthModalOpen: oauth.manualOauthModalOpen,
    manualOauthCallbackValue: oauth.manualOauthCallbackValue,
    manualOauthSubmitting: oauth.manualOauthSubmitting,
    setModalOpen,
    setEditingAccountId,
    setManualOauthModalOpen: oauth.setManualOauthModalOpen,
    setManualOauthCallbackValue: oauth.setManualOauthCallbackValue,
    openAccountModal,
    handleSaveAccount,
    handleAuthorizeAccount,
    handleDeleteAccount,
    handleToggleChat,
    handleSubmitManualOauthCallback: oauth.handleSubmitManualOauthCallback,
  };
}
