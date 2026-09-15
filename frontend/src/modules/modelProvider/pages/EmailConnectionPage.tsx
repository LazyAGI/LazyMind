import { useCallback, useEffect, useState } from "react";
import {
  Alert,
  Button,
  Collapse,
  Form,
  Input,
  Modal,
  Space,
  Tag,
  Tooltip,
  Typography,
  message,
} from "antd";
import { ArrowLeftOutlined, ArrowRightOutlined, MailOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";

import { dataSourceCloudOauthApi } from "@/modules/dataSource/api/clients";
import { unwrapApiData } from "@/modules/dataSource/api/unwrap";
import {
  enableCloudConnectionForChat,
  setCloudConnectionChatEnabled,
} from "@/modules/dataSource/common/feishuOAuth";
import { CLOUD_DOCUMENTS_PATH } from "@/modules/modelProvider/utils/cloudDocumentUrls";
import neteaseLogo from "../assets/mail/netease.png";
import qqmailLogo from "../assets/mail/qqmail.png";
import qqexmailLogo from "../assets/mail/qqexmail.png";
import gmailLogo from "../assets/mail/gmail.png";
import "@/modules/modelProvider/index.scss";
import "./googleDriveConnectionPage.scss";
import "./emailConnectionPage.scss";

const { Paragraph } = Typography;

type MailProvider = "netease163" | "neteaseqiye" | "qqmail" | "qqexmail" | "gmailimap";
type MailRowId = "netease163" | "neteaseqiye" | "qqmail" | "qqexmail" | "gmail";

interface MailConnection {
  connection_id: string;
  provider: MailProvider;
  display_name?: string;
  status?: string;
  last_error?: string;
  scope?: string;
  chatEnabled: boolean;
}

const MAIL_LIST_PROVIDERS: MailProvider[] = [
  "netease163",
  "neteaseqiye",
  "qqmail",
  "qqexmail",
  "gmailimap",
];

const MAIL_ROWS: { id: MailRowId; logo: string; providers: MailProvider[] }[] = [
  { id: "netease163", logo: neteaseLogo, providers: ["netease163"] },
  { id: "neteaseqiye", logo: neteaseLogo, providers: ["neteaseqiye"] },
  { id: "qqmail", logo: qqmailLogo, providers: ["qqmail"] },
  { id: "qqexmail", logo: qqexmailLogo, providers: ["qqexmail"] },
  { id: "gmail", logo: gmailLogo, providers: ["gmailimap"] },
];

const GMAIL_TWO_STEP_URL = "https://myaccount.google.com/signinoptions/two-step-verification";
const GMAIL_APP_PASSWORD_URL = "https://myaccount.google.com/apppasswords";

const NETEASE_PERSONAL_DOMAINS = ["163.com", "126.com", "yeah.net", "vip.163.com", "188.com"];

const GUIDE_STEPS: Record<MailRowId, string[]> = {
  netease163: ["s1", "s2", "s3", "s4", "s5", "s6"],
  neteaseqiye: ["s1", "s2", "s3", "s4", "s5", "s6"],
  qqmail: ["s1", "s2", "s3", "s4", "s5", "s6"],
  qqexmail: ["s1", "s2", "s3", "s4", "s5", "s6"],
  gmail: ["s1", "s2", "s3", "s4", "s5", "s6"],
};

function emailDomain(value: string) {
  return String(value || "").trim().toLowerCase().split("@")[1] || "";
}

function isActiveStatus(status?: string) {
  return String(status || "").toUpperCase() === "ACTIVE";
}

function isMailChatEnabled(item: Record<string, any> | undefined) {
  const options = item?.provider_options || item?.providerOptions || {};
  const meta = item?.provider_account_meta || item?.providerAccountMeta || {};
  const raw =
    options.chat_enabled ?? options.chatEnabled ?? meta.chat_enabled ?? meta.chatEnabled;
  return raw == null ? true : Boolean(raw);
}

function MailLogo({ src, alt }: { src: string; alt: string }) {
  return (
    <span className="model-provider-cloud-doc-resource-logo mail-provider-row-logo">
      <img alt={alt} src={src} className="is-loaded" />
    </span>
  );
}

function GuideText({ text }: { text: string }) {
  const parts = String(text || "").split(/(https?:\/\/[^\s]+)/g);
  return (
    <>
      {parts.map((part, index) =>
        part.startsWith("http") ? (
          <a key={`${part}-${index}`} href={part} target="_blank" rel="noreferrer">
            {part}
          </a>
        ) : (
          part
        ),
      )}
    </>
  );
}

function GuideCollapse({ provider }: { provider: MailRowId }) {
  const { t } = useTranslation();
  return (
    <Collapse
      ghost
      className="mail-provider-guide"
      items={[
        {
          key: "guide",
          label: t(`modelProvider.mail.${provider}.guideTitle`),
          children: (
            <div className="mail-provider-guide-body">
              <p>
                <GuideText text={t(`modelProvider.mail.${provider}.guideIntro`)} />
              </p>
              <ol>
                {GUIDE_STEPS[provider].map((step) => (
                  <li key={step}>
                    <GuideText text={t(`modelProvider.mail.${provider}.guide.${step}`)} />
                  </li>
                ))}
              </ol>
            </div>
          ),
        },
      ]}
    />
  );
}

export default function EmailConnectionPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [formNetease] = Form.useForm<{ email: string; authCode: string }>();
  const [formNeteaseQiye] = Form.useForm<{ email: string; authCode: string }>();
  const [formQQ] = Form.useForm<{ email: string; authCode: string }>();
  const [formQQExmail] = Form.useForm<{ email: string; authCode: string }>();
  const [formGmailImap] = Form.useForm<{ email: string; authCode: string }>();
  const [connections, setConnections] = useState<MailConnection[]>([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState<MailProvider | null>(null);
  const [openRow, setOpenRow] = useState<MailRowId | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const items: MailConnection[] = [];
      for (const provider of MAIL_LIST_PROVIDERS) {
        const response = await dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet({
          provider,
          status: null,
        });
        const data = unwrapApiData<any>(response.data);
        for (const item of data?.items || []) {
          if (String(item.status || "").toUpperCase() === "REVOKED") {
            continue;
          }
          items.push({
            connection_id: item.connection_id,
            provider,
            display_name: item.display_name,
            status: item.status,
            last_error: item.last_error,
            scope: item.scope,
            chatEnabled: isMailChatEnabled(item),
          });
        }
      }
      setConnections(items);
    } catch {
      setConnections([]);
    } finally {
      setLoading(false);
    }
  }, []);

  const connectionFor = (provider: MailProvider) =>
    connections.find((item) => item.provider === provider && isActiveStatus(item.status));
  const connected = MAIL_LIST_PROVIDERS.map((provider) => connectionFor(provider)).filter(
    (item): item is MailConnection => Boolean(item),
  );

  const toggleChat = async (connection: MailConnection, enabled: boolean) => {
    try {
      await setCloudConnectionChatEnabled(connection.connection_id, enabled);
      await refresh();
    } catch (error: any) {
      message.error(error?.message || t("modelProvider.mail.chatSwitchFailed"));
    }
  };

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const disconnect = async (connectionId: string) => {
    setLoading(true);
    try {
      await dataSourceCloudOauthApi.deleteConnectionApiAuthserviceV1CloudConnectionsConnectionIdDelete({
        connectionId,
      });
      await refresh();
      message.success(t("modelProvider.mail.disconnected"));
    } catch (error: any) {
      message.error(error?.message || t("modelProvider.mail.disconnectFailed"));
    } finally {
      setLoading(false);
    }
  };

  const connectImap = async (
    provider: MailProvider,
    form: ReturnType<typeof Form.useForm<{ email: string; authCode: string }>>[0],
  ) => {
    try {
      const values = await form.validateFields();
      setSaving(provider);
      const response = await dataSourceCloudOauthApi.createConnectionApiAuthserviceV1CloudProviderConnectionsPost({
        provider,
        cloudConnectionCreateBody: {
          auth_mode: "service_account",
          client_id: values.email.trim(),
          client_secret: values.authCode,
          provider_options: { chat_enabled: true, chatEnabled: true },
        },
      });
      const data = unwrapApiData<any>(response.data);
      if (data?.connection_id) {
        await enableCloudConnectionForChat(data.connection_id);
      }
      form.resetFields();
      await refresh();
      message.success(t("modelProvider.mail.connected"));
    } catch (error: any) {
      if (error?.errorFields) {
        return;
      }
      message.error(error?.message || t("modelProvider.mail.connectFailed"));
    } finally {
      setSaving(null);
    }
  };

  const stopRowOpen = (event: { stopPropagation: () => void }) => {
    event.stopPropagation();
  };

  const chatSwitch = (provider: MailProvider, compactLabel?: string) => {
    const connection = connectionFor(provider);
    const canToggle = Boolean(connection);
    const enabled = Boolean(connection?.chatEnabled);
    const label = connection?.display_name || t(`modelProvider.mail.providers.${provider}`);
    return (
      <div className="mail-provider-row-switch" onClick={stopRowOpen} onKeyDown={stopRowOpen}>
        {compactLabel ? <span className="mail-provider-row-switch-name">{compactLabel}</span> : null}
        <Tooltip
          title={
            canToggle
              ? t("modelProvider.mail.chatSwitchHint")
              : t("modelProvider.mail.chatSwitchDisabled")
          }
        >
          <button
            type="button"
            role="switch"
            aria-checked={enabled}
            aria-disabled={!canToggle}
            aria-label={t("modelProvider.mail.chatSwitchAria", { name: label })}
            disabled={!canToggle || loading}
            className={`model-provider-cloud-doc-switch${enabled ? " is-on" : ""}${
              canToggle ? "" : " is-disabled"
            }`}
            onClick={() => {
              if (connection) {
                void toggleChat(connection, !enabled);
              }
            }}
          >
            <span className="model-provider-cloud-doc-switch-thumb" aria-hidden="true" />
            <span className="model-provider-cloud-doc-switch-label">
              {enabled
                ? t("admin.dataSourceFeishuAccountChatOn")
                : t("admin.dataSourceFeishuAccountChatOff")}
            </span>
          </button>
        </Tooltip>
      </div>
    );
  };

  const disconnectButton = (provider: MailProvider) => {
    const connection = connectionFor(provider);
    if (!connection) {
      return null;
    }
    return (
      <Button danger loading={loading} onClick={() => void disconnect(connection.connection_id)}>
        {t("modelProvider.mail.disconnect")}
      </Button>
    );
  };

  const rowHint = (row: (typeof MAIL_ROWS)[number]) => {
    const accounts = row.providers
      .map((provider) => connectionFor(provider))
      .filter((item): item is MailConnection => Boolean(item));
    if (!accounts.length) {
      return t(`modelProvider.mail.${row.id}.summary`);
    }
    return t("modelProvider.mail.connectedHint", {
      account: accounts
        .map((item) => {
          const name = t(`modelProvider.mail.providers.${item.provider}`);
          return item.display_name ? `${name} ${item.display_name}` : name;
        })
        .join(" · "),
    });
  };

  const renderImapForm = (
    provider: MailProvider,
    form: ReturnType<typeof Form.useForm<{ email: string; authCode: string }>>[0],
    options?: {
      emailPlaceholder?: string;
      domainError?: string;
      allowedDomains?: string[];
      authLabel?: string;
      submitLabel: string;
    },
  ) => (
    <Form form={form} layout="vertical" className="mail-provider-form">
      <Form.Item
        name="email"
        label={t("modelProvider.mail.email")}
        rules={[
          { required: true, type: "email" },
          ...(options?.allowedDomains
            ? [
                {
                  validator: async (_: unknown, value: string) => {
                    if (value && !options.allowedDomains!.includes(emailDomain(value))) {
                      return Promise.reject(options.domainError);
                    }
                  },
                },
              ]
            : provider === "qqmail"
              ? [
                  {
                    validator: async (_: unknown, value: string) => {
                      const domain = emailDomain(value);
                      if (value && domain !== "qq.com" && domain !== "foxmail.com") {
                        return Promise.reject(t("modelProvider.mail.qqmail.domainError"));
                      }
                    },
                  },
                ]
              : []),
        ]}
      >
        <Input autoComplete="username" placeholder={options?.emailPlaceholder} />
      </Form.Item>
      <Form.Item
        name="authCode"
        label={options?.authLabel || t("modelProvider.mail.authCode")}
        rules={[{ required: true }]}
      >
        <Input.Password autoComplete="new-password" />
      </Form.Item>
      <Space wrap>
        <Button type="primary" loading={saving === provider} onClick={() => void connectImap(provider, form)}>
          {options?.submitLabel}
        </Button>
        {disconnectButton(provider)}
      </Space>
    </Form>
  );

  const renderModalBody = (rowId: MailRowId) => {
    if (rowId === "netease163") {
      return (
        <>
          {renderImapForm("netease163", formNetease, {
            emailPlaceholder: t("modelProvider.mail.netease163.emailPlaceholder"),
            domainError: t("modelProvider.mail.netease163.domainError"),
            allowedDomains: NETEASE_PERSONAL_DOMAINS,
            submitLabel: t("modelProvider.mail.connectNetease"),
          })}
          <GuideCollapse provider="netease163" />
        </>
      );
    }
    if (rowId === "neteaseqiye") {
      return (
        <>
          {renderImapForm("neteaseqiye", formNeteaseQiye, {
            emailPlaceholder: t("modelProvider.mail.neteaseqiye.emailPlaceholder"),
            submitLabel: t("modelProvider.mail.connectNeteaseQiye"),
          })}
          <GuideCollapse provider="neteaseqiye" />
        </>
      );
    }
    if (rowId === "qqmail") {
      return (
        <>
          {renderImapForm("qqmail", formQQ, {
            emailPlaceholder: t("modelProvider.mail.qqmail.emailPlaceholder"),
            submitLabel: t("modelProvider.mail.connectQQ"),
          })}
          <GuideCollapse provider="qqmail" />
        </>
      );
    }
    if (rowId === "qqexmail") {
      return (
        <>
          {renderImapForm("qqexmail", formQQExmail, {
            emailPlaceholder: t("modelProvider.mail.qqexmail.emailPlaceholder"),
            submitLabel: t("modelProvider.mail.connectQQExmail"),
          })}
          <GuideCollapse provider="qqexmail" />
        </>
      );
    }
    return (
      <>
        <Alert
          showIcon
          type="info"
          className="mail-provider-gmail-links"
          message={t("modelProvider.mail.gmail.setupTitle")}
          description={
            <div>
              <p>{t("modelProvider.mail.gmail.setupHint")}</p>
              <p>
                <a href={GMAIL_TWO_STEP_URL} target="_blank" rel="noreferrer">
                  {t("modelProvider.mail.gmail.twoStepLabel")}
                </a>
                <span> {GMAIL_TWO_STEP_URL}</span>
              </p>
              <p>
                <a href={GMAIL_APP_PASSWORD_URL} target="_blank" rel="noreferrer">
                  {t("modelProvider.mail.gmail.appPasswordLabel")}
                </a>
                <span> {GMAIL_APP_PASSWORD_URL}</span>
              </p>
            </div>
          }
        />
        {renderImapForm("gmailimap", formGmailImap, {
          emailPlaceholder: t("modelProvider.mail.gmail.emailPlaceholder"),
          authLabel: t("modelProvider.mail.gmail.imapPassword"),
          submitLabel: t("modelProvider.mail.connectGmailImap"),
        })}
        <GuideCollapse provider="gmail" />
      </>
    );
  };

  return (
    <div className="google-drive-provider-page mail-provider-page">
      <header className="google-drive-provider-header">
        <Button type="link" icon={<ArrowLeftOutlined />} onClick={() => navigate(CLOUD_DOCUMENTS_PATH)}>
          {t("modelProvider.mail.back")}
        </Button>
        <div className="google-drive-provider-heading">
          <span aria-hidden="true">
            <MailOutlined />
          </span>
          <div>
            <h1>{t("modelProvider.mail.title")}</h1>
            <Paragraph>{t("modelProvider.mail.description")}</Paragraph>
          </div>
        </div>
      </header>
      <main className="google-drive-provider-content">
        {connected.length ? (
          <Alert
            showIcon
            type="success"
            message={t("modelProvider.mail.connectedCount", { count: connected.length })}
            description={t("modelProvider.mail.multiEnableHint", {
              accounts: connected
                .map((item) =>
                  `${t(`modelProvider.mail.providers.${item.provider}`)} ${item.display_name || ""}`.trim(),
                )
                .join("、"),
            })}
          />
        ) : (
          <Alert showIcon type="info" message={t("modelProvider.mail.empty")} />
        )}

        <div className="model-provider-cloud-doc-grid mail-provider-list">
          {MAIL_ROWS.map((row) => {
            const active = row.providers.some((provider) => connectionFor(provider));
            return (
              <div
                key={row.id}
                className={`model-provider-cloud-doc-resource-row mail-provider-row${
                  active ? " is-active" : ""
                }`}
                role="button"
                tabIndex={0}
                onClick={() => setOpenRow(row.id)}
                onKeyDown={(event) => {
                  if (event.key === "Enter" || event.key === " ") {
                    event.preventDefault();
                    setOpenRow(row.id);
                  }
                }}
              >
                <MailLogo src={row.logo} alt={t(`modelProvider.mail.${row.id}.title`)} />
                <div className="model-provider-cloud-doc-resource-copy">
                  <h3>{t(`modelProvider.mail.${row.id}.title`)}</h3>
                  <p>{rowHint(row)}</p>
                </div>
                <Tag
                  className="model-provider-cloud-doc-resource-status"
                  color={active ? "success" : "default"}
                >
                  {active
                    ? t("modelProvider.cloudDocuments.authValid")
                    : t("modelProvider.cloudDocuments.authPending")}
                </Tag>
                <div className="mail-provider-row-controls">
                  {chatSwitch(row.providers[0])}
                  <button
                    type="button"
                    className="model-provider-cloud-doc-resource-action"
                    onClick={(event) => {
                      event.stopPropagation();
                      setOpenRow(row.id);
                    }}
                  >
                    {active
                      ? t("modelProvider.cloudDocuments.manageAccount")
                      : t("modelProvider.cloudDocuments.configureConnection")}
                    <ArrowRightOutlined />
                  </button>
                </div>
              </div>
            );
          })}
        </div>
      </main>

      <Modal
        className="mail-provider-modal"
        width={720}
        open={Boolean(openRow)}
        title={openRow ? t(`modelProvider.mail.${openRow}.title`) : undefined}
        footer={null}
        destroyOnHidden
        onCancel={() => setOpenRow(null)}
      >
        {openRow ? (
          <div className="mail-provider-modal-body">
            <p className="mail-provider-modal-summary">{t(`modelProvider.mail.${openRow}.summary`)}</p>
            {renderModalBody(openRow)}
          </div>
        ) : null}
      </Modal>
    </div>
  );
}
