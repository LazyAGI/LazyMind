import { useCallback, useEffect, useState } from "react";
import { Button, Form, Input, Modal, Space, Tag, message } from "antd";
import { DeleteOutlined, FileTextOutlined, GoogleOutlined, LinkOutlined, SettingOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";

import { dataSourceCloudOauthApi } from "@/modules/dataSource/api/clients";
import { unwrapApiData } from "@/modules/dataSource/api/unwrap";
import { getCloudDataSourceCallbackUrl } from "@/modules/dataSource/oauth/urls";
import {
  CLOUD_DATA_SOURCE_OAUTH_CHANNEL,
  consumeCloudDataSourceOAuthResult,
  enableCloudConnectionForChat,
  openCenteredPopup,
  requestCloudDataSourceAuthorizeUrl,
  type CloudDataSourceOAuthMessage,
} from "@/modules/dataSource/common/feishuOAuth";
import { CLOUD_DOCUMENTS_GOOGLE_DRIVE_SETUP_PATH } from "@/modules/modelProvider/utils/cloudDocumentUrls";


interface GoogleDriveAppForm {
  clientId: string;
  clientSecret?: string;
}

interface GoogleDriveConnection {
  connection_id: string;
  display_name?: string;
  provider_account_meta?: Record<string, unknown>;
}


export default function GoogleDriveConnectionSection() {
  const { t } = useTranslation();
  const [form] = Form.useForm<GoogleDriveAppForm>();
  const [connection, setConnection] = useState<GoogleDriveConnection | null>(null);
  const [loading, setLoading] = useState(false);
  const [modalOpen, setModalOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [secretConfigured, setSecretConfigured] = useState(false);
  const callbackUrl = getCloudDataSourceCallbackUrl("googledrive");

  const refreshConnection = useCallback(async () => {
    setLoading(true);
    try {
      const response =
        await dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet({
          provider: "googledrive",
          authMode: "oauth_user",
          status: "ACTIVE",
        });
      const data = unwrapApiData<any>(response.data);
      setConnection((data?.items || [])[0] || null);
    } catch {
      setConnection(null);
    } finally {
      setLoading(false);
    }
  }, []);

  const finishConnection = useCallback(
    async (payload: CloudDataSourceOAuthMessage) => {
      if (payload.status !== "success" || payload.connection.provider !== "googledrive") {
        return;
      }
      await enableCloudConnectionForChat(payload.connection.connectionId);
      await refreshConnection();
      message.success(t("modelProvider.external.googleDriveConnected"));
    },
    [refreshConnection, t],
  );

  useEffect(() => {
    void refreshConnection();
    const stored = consumeCloudDataSourceOAuthResult("googledrive");
    if (stored) {
      void finishConnection(stored);
    }

    const handleOAuthMessage = (event: MessageEvent<CloudDataSourceOAuthMessage>) => {
      if (
        event.origin === window.location.origin &&
        event.data?.channel === CLOUD_DATA_SOURCE_OAUTH_CHANNEL
      ) {
        void finishConnection(event.data);
      }
    };
    window.addEventListener("message", handleOAuthMessage);
    return () => window.removeEventListener("message", handleOAuthMessage);
  }, [finishConnection, refreshConnection]);

  const openConfiguration = async () => {
    form.resetFields();
    try {
      const response =
        await dataSourceCloudOauthApi.getOauthAppCredentialsApiAuthserviceV1CloudProviderOauthAppCredentialsGet({
          provider: "googledrive",
        });
      const data = unwrapApiData<any>(response.data);
      form.setFieldsValue({ clientId: data?.app_id || "", clientSecret: "" });
      setSecretConfigured(Boolean(data?.secret_configured));
    } catch {
      form.setFieldsValue({ clientId: "", clientSecret: "" });
      setSecretConfigured(false);
    }
    setModalOpen(true);
  };

  const connect = async () => {
    try {
      const values = await form.validateFields();
      const clientSecret = values.clientSecret?.trim() || "";
      setSaving(true);
      await dataSourceCloudOauthApi.saveOauthAppCredentialsApiAuthserviceV1CloudProviderOauthAppCredentialsPut({
        provider: "googledrive",
        cloudOAuthAppCredentialBody: {
          client_id: values.clientId.trim(),
          client_secret: clientSecret || undefined,
        },
      });
      const authorizeUrl = await requestCloudDataSourceAuthorizeUrl("googledrive", {
        tenantId: "",
        appId: values.clientId.trim(),
        appSecret: clientSecret,
        scopes: [],
        returnUrl: window.location.href,
      });
      setModalOpen(false);
      const popup = openCenteredPopup(
        authorizeUrl,
        t("modelProvider.external.googleDriveOAuthWindowTitle"),
      );
      if (!popup) {
        window.location.assign(authorizeUrl);
      }
    } catch (error: any) {
      if (error?.errorFields) {
        return;
      }
      message.error(error?.message || t("modelProvider.external.googleDriveConnectFailed"));
    } finally {
      setSaving(false);
    }
  };

  const disconnect = async () => {
    if (!connection?.connection_id) {
      return;
    }
    setLoading(true);
    try {
      await dataSourceCloudOauthApi.deleteConnectionApiAuthserviceV1CloudConnectionsConnectionIdDelete({
        connectionId: connection.connection_id,
      });
      setConnection(null);
      message.success(t("modelProvider.external.googleDriveDisconnected"));
    } catch (error: any) {
      message.error(error?.message || t("modelProvider.external.googleDriveDisconnectFailed"));
    } finally {
      setLoading(false);
    }
  };

  const meta = connection?.provider_account_meta || {};
  const accountName =
    connection?.display_name ||
    String(meta.display_name || meta.email || "") ||
    t("modelProvider.external.googleDriveAccountFallback");

  return (
    <>
      <section className="model-provider-service-category google-drive-connection-section">
        <div className="model-provider-service-category-top">
          <div className="model-provider-service-category-head">
            <span><GoogleOutlined /></span>
            <div>
              <h3>{t("modelProvider.external.googleDriveTitle")}</h3>
              <p>{t("modelProvider.external.googleDriveDesc")}</p>
            </div>
          </div>
          <Space>
            {connection
              ? <Tag color="success">{accountName}</Tag>
              : <Tag>{t("modelProvider.external.status.missing")}</Tag>}
            <Button
              icon={connection ? <SettingOutlined /> : <LinkOutlined />}
              loading={loading}
              onClick={() => void openConfiguration()}
              type={connection ? "default" : "primary"}
            >
              {connection
                ? t("modelProvider.external.googleDriveReconnect")
                : t("modelProvider.external.googleDriveConnect")}
            </Button>
            {connection ? (
              <Button danger icon={<DeleteOutlined />} loading={loading} onClick={() => void disconnect()}>
                {t("modelProvider.external.googleDriveDisconnect")}
              </Button>
            ) : null}
          </Space>
        </div>
      </section>

      <Modal
        destroyOnClose
        open={modalOpen}
        title={t("modelProvider.external.googleDriveConfigTitle")}
        okText={t("modelProvider.external.googleDriveAuthorize")}
        confirmLoading={saving}
        onCancel={() => setModalOpen(false)}
        onOk={() => void connect()}
      >
        <Form form={form} layout="vertical">
          <Form.Item
            label="OAuth Client ID"
            name="clientId"
            rules={[{ required: true, message: t("modelProvider.external.googleDriveClientIdRequired") }]}
          >
            <Input autoComplete="off" />
          </Form.Item>
          <Form.Item
            label="OAuth Client Secret"
            name="clientSecret"
            rules={[{
              required: !secretConfigured,
              message: t("modelProvider.external.googleDriveClientSecretRequired"),
            }]}
          >
            <Input.Password
              autoComplete="new-password"
              placeholder={secretConfigured ? t("modelProvider.external.googleDriveSecretConfigured") : undefined}
            />
          </Form.Item>
          <p className="google-drive-connection-hint">
            {t("modelProvider.external.googleDriveConfigHint", { callbackUrl })}
          </p>
          <p className="google-drive-connection-guide">
            <a
              href={`${CLOUD_DOCUMENTS_GOOGLE_DRIVE_SETUP_PATH}?from=google-drive-provider`}
              target="_blank"
              rel="noreferrer"
            >
              <FileTextOutlined /> {t("modelProvider.external.googleDriveSetupGuideAction")}
            </a>
          </p>
        </Form>
      </Modal>
    </>
  );
}
