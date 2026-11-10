import {
  CheckCircleOutlined,
  ExclamationCircleOutlined,
} from "@ant-design/icons";
import {
  Alert,
  Button,
  Modal,
  Space,
  Switch,
  Tag,
  Typography,
  message,
} from "antd";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  ankiIntegrationStatus,
  openAnki,
  type DesktopAnkiStatus,
} from "@/runtime/desktopBridge";
import {
  getAnkiStatus,
  getVocabularyProvider,
  requestAnkiPermission,
  saveVocabularyProvider,
  type AnkiProviderStatus,
  type VocabularyProviderSetting,
} from "./api";
import "@/modules/agentIntegration/index.scss";
import CloudVocabularyConnections from "./CloudVocabularyConnections";
import ankiLogo from "./assets/anki.png";

const ADDON_CODE = "2055492159";

export default function VocabularySettings() {
  const { t } = useTranslation();
  const [desktop, setDesktop] = useState<DesktopAnkiStatus | null>(null);
  const [status, setStatus] = useState<AnkiProviderStatus | null>(null);
  const [setting, setSetting] = useState<VocabularyProviderSetting | null>(
    null,
  );
  const [busy, setBusy] = useState(false);
  const [guide, setGuide] = useState<"anki" | "connect" | null>(null);
  const [configureOpen, setConfigureOpen] = useState(false);

  const refresh = useCallback(async () => {
    const [nextDesktop, nextStatus, nextSetting] = await Promise.all([
      ankiIntegrationStatus().catch(() => null),
      getAnkiStatus().catch(() => null),
      getVocabularyProvider(),
    ]);
    setDesktop(nextDesktop);
    setStatus(nextStatus);
    setSetting(nextSetting);
  }, []);
  useEffect(() => {
    void refresh();
  }, [refresh]);

  const run = async (action: () => Promise<unknown>, success?: string) => {
    setBusy(true);
    try {
      await action();
      if (success) message.success(success);
      await refresh();
    } catch (error) {
      message.error(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  };
  const installConnect = async () => {
    await navigator.clipboard?.writeText(ADDON_CODE).catch(() => undefined);
    await openAnki().catch(() => undefined);
    setGuide("connect");
  };
  const toggleConnection = async (enabled: boolean) => {
    if (!setting) return;
    await run(
      async () => {
        if (enabled) await requestAnkiPermission();
        await saveVocabularyProvider({
          ...setting,
          selected_provider: enabled ? "anki" : "local",
        });
      },
      enabled ? t("ankiSettings.linked") : t("ankiSettings.unlinked"),
    );
  };
  const installed = Boolean(desktop?.installed);
  const connectInstalled = Boolean(desktop?.connect_installed);
  const connectReady = Boolean(status?.connected);
  const enabled = connectReady && setting?.selected_provider === "anki";

  return (
    <div className="external-apps-settings">
      <div className="external-apps-grid">
        <article className="external-app-card">
          <span className="external-app-icon"><img src={ankiLogo} alt="" aria-hidden="true" /></span>
          <div className="external-app-copy">
            <h3>Anki</h3>
            <p>{t("ankiSettings.appDescription")}</p>
          </div>
          <Tag className="external-app-status" color={enabled ? "success" : "default"}>
            {t(!setting || !status ? "vocabularyCloud.unavailable" : enabled ? "vocabularyCloud.connectedEnabled" : connectReady ? "vocabularyCloud.connectedPaused" : "vocabularyCloud.incomplete")}
          </Tag>
          <Button aria-label={`Anki ${t("vocabularyCloud.manage")}`} onClick={() => setConfigureOpen(true)}>{t("vocabularyCloud.manage")}</Button>
        </article>
        <CloudVocabularyConnections />
      </div>
      <Modal open={configureOpen} title="Anki" onCancel={() => setConfigureOpen(false)} footer={null} width={680} styles={{ body: { maxHeight: "75vh", overflowY: "auto" } }}>
        <div className="agent-integration-page">
          <div className="agent-integration-flow">
            <ConnectionStage title={t("ankiSettings.clientSetup")} ready={installed}>
              <StatusLine
                ready={installed}
                text={
                  installed
                    ? t("ankiSettings.clientInstalled")
                    : t("ankiSettings.clientNotFound")
                }
              />
              <Space style={{ marginTop: 14 }}>
                {installed ? (
                  <Button onClick={() => void run(openAnki)}>
                    {t("ankiSettings.openAnki")}
                  </Button>
                ) : (
                  <Button
                    type="primary"
                    onClick={() => setGuide("anki")}
                  >
                    {t("ankiSettings.viewGuide")}
                  </Button>
                )}
              </Space>
            </ConnectionStage>
            <ConnectionStage
              title={t("ankiSettings.connectSetup")}
              ready={connectReady}
            >
              <StatusLine
                ready={connectReady}
                text={
                  connectReady
                    ? (status?.version ? t("ankiSettings.connectReadyVersion", { version: status.version }) : t("ankiSettings.connectReady"))
                    : t("ankiSettings.connectMissing")
                }
              />
              {!connectReady && installed ? (
                <Space style={{ marginTop: 14 }}>
                  {connectInstalled ? (
                    <Button
                      type="primary"
                      onClick={() =>
                        void run(
                          openAnki,
                          t("ankiSettings.ankiOpened"),
                        )
                      }
                    >
                      {t("ankiSettings.openAnki")}
                    </Button>
                  ) : (
                    <Button
                      type="primary"
                      onClick={() => void installConnect()}
                    >
                      {t("ankiSettings.installConnect")}
                    </Button>
                  )}
                  <Button
                    loading={busy}
                    onClick={() => void run(refresh)}
                  >
                    {t("ankiSettings.checkAgain")}
                  </Button>
                </Space>
              ) : null}
            </ConnectionStage>
            <ConnectionStage title={t("ankiSettings.connection")} ready={enabled}>
              <div
                className={`agent-integration-compact-control${enabled ? " is-enabled" : ""}`}
              >
                <div>
                  <strong>{t("ankiSettings.allow")}</strong>
                  <Typography.Paragraph type="secondary">
                    {t("ankiSettings.allowDescription")}
                  </Typography.Paragraph>
                </div>
                <Switch
                  checked={enabled}
                  disabled={!connectReady || busy}
                  loading={busy}
                  onChange={(value) => void toggleConnection(value)}
                />
              </div>
            </ConnectionStage>
          </div>
          {installed && connectInstalled && !connectReady ? (
            <Alert
              style={{ marginTop: 14 }}
              type="info"
              showIcon
              message={t("ankiSettings.startAnki")}
            />
          ) : status?.message && installed && !connectReady ? (
            <Alert
              style={{ marginTop: 14 }}
              type="info"
              showIcon
              message={status.message}
            />
          ) : null}
        </div>
      </Modal>
      <Modal
        open={guide === "anki"}
        title={t("ankiSettings.installClient")}
        onCancel={() => setGuide(null)}
        footer={
          <Button
            type="primary"
            href="https://apps.ankiweb.net/"
            target="_blank"
          >
            {t("ankiSettings.downloadAnki")}
          </Button>
        }
      >
        <ol>
          <li>{t("ankiSettings.downloadGuide")}</li>
          <li>{t("ankiSettings.launchGuide")}</li>
          <li>{t("ankiSettings.checkGuide")}</li>
        </ol>
      </Modal>
      <Modal
        open={guide === "connect"}
        title={t("ankiSettings.installConnect")}
        onCancel={() => setGuide(null)}
        footer={
          <Button
            type="primary"
            onClick={() => {
              setGuide(null);
              void run(refresh);
            }}
          >
            {t("ankiSettings.checkAfterInstall")}
          </Button>
        }
      >
        <Alert
          type="warning"
          showIcon
          message={t("ankiSettings.addonWarning")}
        />
        <ol>
          <li>{t("ankiSettings.codeCopied")}</li>
          <li>{t("ankiSettings.openAddons")}</li>
          <li>{t("ankiSettings.pasteCode", { code: ADDON_CODE })}</li>
          <li>{t("ankiSettings.restartGuide")}</li>
        </ol>
        <Typography.Paragraph copyable={{ text: ADDON_CODE }}>
          <Typography.Text code>{ADDON_CODE}</Typography.Text>
        </Typography.Paragraph>
      </Modal>
    </div>
  );
}

function ConnectionStage({
  title,
  ready,
  children,
}: {
  title: string;
  ready: boolean;
  children: React.ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <section className={`agent-integration-stage${ready ? " is-ready" : ""}`}>
      <div className="agent-integration-stage-rail">
        <span />
      </div>
      <div className="agent-integration-stage-copy">
        <div className="agent-integration-stage-heading">
          <div>
            <strong>{title}</strong>
          </div>
          <span>{ready ? t("ankiSettings.ready") : t("ankiSettings.pending")}</span>
        </div>
        <div className="agent-integration-stage-content">{children}</div>
      </div>
    </section>
  );
}

function StatusLine({ ready, text }: { ready: boolean; text: string }) {
  return (
    <div className="agent-integration-requirements">
      <div>
        {ready ? (
          <CheckCircleOutlined style={{ color: "#16a05d" }} />
        ) : (
          <ExclamationCircleOutlined style={{ color: "#ef5b4c" }} />
        )}
        <span>{text}</span>
      </div>
    </div>
  );
}
