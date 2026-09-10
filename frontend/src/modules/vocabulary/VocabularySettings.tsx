import {
  CheckCircleOutlined,
  DownOutlined,
  ExclamationCircleOutlined,
} from "@ant-design/icons";
import {
  Alert,
  Button,
  Card,
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

const ADDON_CODE = "2055492159";

export default function VocabularySettings() {
  const { i18n } = useTranslation();
  const zh = String(
    i18n.resolvedLanguage || i18n.language || "zh-CN",
  ).startsWith("zh");
  const [desktop, setDesktop] = useState<DesktopAnkiStatus | null>(null);
  const [status, setStatus] = useState<AnkiProviderStatus | null>(null);
  const [setting, setSetting] = useState<VocabularyProviderSetting | null>(
    null,
  );
  const [busy, setBusy] = useState(false);
  const [guide, setGuide] = useState<"anki" | "connect" | null>(null);
  const [expanded, setExpanded] = useState(true);

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
      enabled ? "LazyMind 已连接 Anki" : "LazyMind 已停止操作 Anki",
    );
  };
  const installed = Boolean(desktop?.installed);
  const connectInstalled = Boolean(desktop?.connect_installed);
  const connectReady = Boolean(status?.connected);
  const enabled = connectReady && setting?.selected_provider === "anki";

  return (
    <div className="agent-integration-page">
      <section className="agent-integration-section">
        <div className="agent-integration-grid">
          <div className="agent-integration-column">
            <Card
              className={`agent-integration-card${expanded ? " is-expanded" : ""}`}
            >
              <div className="agent-integration-card-header">
                <button
                  type="button"
                  className="agent-integration-card-toggle"
                  aria-expanded={expanded}
                  onClick={() => setExpanded((value) => !value)}
                >
                  <span className="agent-integration-identity">
                    <Typography.Title level={4}>Anki</Typography.Title>
                  </span>
                </button>
                {!expanded &&
                  (installed && connectReady ? (
                    <div className="agent-integration-compact-controls">
                      <div
                        className={`agent-integration-compact-control${enabled ? " is-enabled" : ""}`}
                      >
                        <span>允许 LazyMind 操作 Anki</span>
                        <Switch
                          size="small"
                          checked={enabled}
                          disabled={busy}
                          loading={busy}
                          onChange={(value) => void toggleConnection(value)}
                        />
                      </div>
                    </div>
                  ) : (
                    <div className="agent-integration-compact-detection">
                      <span className={installed ? "is-ready" : ""}>
                        {installed ? (
                          <CheckCircleOutlined />
                        ) : (
                          <ExclamationCircleOutlined />
                        )}
                        <span>
                          {installed
                            ? "Anki 客户端已安装"
                            : "Anki 客户端未安装"}
                        </span>
                      </span>
                      <span className={connectReady ? "is-ready" : ""}>
                        {connectReady ? (
                          <CheckCircleOutlined />
                        ) : (
                          <ExclamationCircleOutlined />
                        )}
                        <span>
                          {connectReady
                            ? "AnkiConnect 已就绪"
                            : "AnkiConnect 未就绪"}
                        </span>
                      </span>
                    </div>
                  ))}
                <Tag
                  className={`agent-integration-install-tag ${installed ? "is-installed" : "is-missing"}`}
                >
                  {installed ? "已安装" : "未安装"}
                </Tag>
                <button
                  type="button"
                  className="agent-integration-expand-button"
                  aria-label={expanded ? "收起" : "展开"}
                  aria-expanded={expanded}
                  onClick={() => setExpanded((value) => !value)}
                >
                  <DownOutlined
                    className="agent-integration-chevron"
                    aria-hidden="true"
                  />
                </button>
              </div>
              {expanded ? (
                <div className="agent-integration-card-detail">
                  <div className="agent-integration-flow">
                    <ConnectionStage title="Anki 客户端准备" ready={installed}>
                      <StatusLine
                        ready={installed}
                        text={
                          installed
                            ? "Anki 客户端已安装"
                            : "尚未找到 Anki 客户端"
                        }
                      />
                      <Space style={{ marginTop: 14 }}>
                        {installed ? (
                          <Button onClick={() => void run(openAnki)}>
                            打开 Anki
                          </Button>
                        ) : (
                          <Button
                            type="primary"
                            onClick={() => setGuide("anki")}
                          >
                            查看安装教程
                          </Button>
                        )}
                      </Space>
                    </ConnectionStage>
                    <ConnectionStage
                      title="AnkiConnect 准备"
                      ready={connectReady}
                    >
                      <StatusLine
                        ready={connectReady}
                        text={
                          connectReady
                            ? `AnkiConnect 已就绪${status?.version ? `（API v${status.version}）` : ""}`
                            : "尚未连接 AnkiConnect"
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
                                  "Anki 已打开，请稍后重新检测",
                                )
                              }
                            >
                              打开 Anki
                            </Button>
                          ) : (
                            <Button
                              type="primary"
                              onClick={() => void installConnect()}
                            >
                              安装 AnkiConnect
                            </Button>
                          )}
                          <Button
                            loading={busy}
                            onClick={() => void run(refresh)}
                          >
                            重新检测
                          </Button>
                        </Space>
                      ) : null}
                    </ConnectionStage>
                    <ConnectionStage title="连接方式" ready={enabled}>
                      <div
                        className={`agent-integration-compact-control${enabled ? " is-enabled" : ""}`}
                      >
                        <div>
                          <strong>允许 LazyMind 操作 Anki</strong>
                          <Typography.Paragraph type="secondary">
                            开启后，LazyMind 可以向 Anki 添加、更新和同步生词。
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
                      message="AnkiConnect 已安装，但 Anki 尚未运行或仍在启动。请打开 Anki 后重新检测。"
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
              ) : null}
            </Card>
          </div>
        </div>
      </section>
      <Modal
        open={guide === "anki"}
        title="安装 Anki 客户端"
        onCancel={() => setGuide(null)}
        footer={
          <Button
            type="primary"
            href="https://apps.ankiweb.net/"
            target="_blank"
          >
            前往下载 Anki
          </Button>
        }
      >
        <ol>
          <li>从 Anki 官方网站下载适合当前系统的客户端。</li>
          <li>完成安装并至少启动一次 Anki。</li>
          <li>返回此页点击“重新检测”。</li>
        </ol>
      </Modal>
      <Modal
        open={guide === "connect"}
        title={zh ? "安装 AnkiConnect" : "Install AnkiConnect"}
        onCancel={() => setGuide(null)}
        footer={
          <Button
            type="primary"
            onClick={() => {
              setGuide(null);
              void run(refresh);
            }}
          >
            完成后重新检测
          </Button>
        }
      >
        <Alert
          type="warning"
          showIcon
          message="Anki 插件可以执行代码，安装必须由你在 Anki 中确认。LazyMind 不直接写入 Anki 的插件目录。"
        />
        <ol>
          <li>Anki 已打开，插件码已复制。</li>
          <li>打开“工具 → 插件 → 获取插件”。</li>
          <li>粘贴插件码 {ADDON_CODE} 并确认。</li>
          <li>重启 Anki，然后返回重新检测。</li>
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
          <span>{ready ? "已完成" : "待完成"}</span>
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
