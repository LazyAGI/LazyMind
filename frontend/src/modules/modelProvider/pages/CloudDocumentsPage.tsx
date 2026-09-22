import { useEffect, useRef, useState } from "react";
import {
  ArrowRightOutlined,
  CheckOutlined,
  FolderOpenOutlined,
  GoogleOutlined,
  MessageOutlined,
  QuestionOutlined,
} from "@ant-design/icons";
import { Alert, Button, Modal, Skeleton, Space, Tooltip } from "antd";
import { useTranslation } from "react-i18next";
import { Link, useSearchParams } from "react-router-dom";
import type { CloudConnectionResponse } from "@/api/generated/auth-client";
import { dataSourceCloudOauthApi } from "@/modules/dataSource/api/clients";
import { unwrapApiData } from "@/modules/dataSource/api/unwrap";
import { enableCloudConnectionForChat, requestCloudDataSourceAuthorizeUrl } from "@/modules/dataSource/oauth/api";
import CloudDocumentProviderPanel, {
  CloudDocumentModals,
} from "../components/CloudDocumentProviderPanel";
import {
  cloudAuthProviderOptions,
  cloudProviderOptions,
  type CloudProviderType,
} from "../constants/cloudProviderOptions";
import { useCloudDocumentProviders } from "../hooks/useCloudDocumentProviders";
import { getCloudKnowledgeCreatePath } from "../utils/cloudDocumentKnowledge";
import {
  CLOUD_DOCUMENT_CONNECTION_SUCCESS_EVENT,
  consumeCloudDocumentConnectionSuccess,
  type CloudDocumentGuideProvider,
} from "../utils/cloudDocumentOnboarding";

const CLOUD_DOCUMENTS_ONBOARDING_KEY =
  "lazymind.cloud-documents.onboarding.v2";
const CHAT_PATH = "/agent/chat/home";

type GuideStage = "roadmap" | "sourceChoice" | "success";

// Query parameters select an account; the authenticated detail API still owns access control.
function ConnectionRecovery({ provider, connectionId }: { provider: string; connectionId: string }) {
  const { t } = useTranslation();
  const [connection, setConnection] = useState<CloudConnectionResponse | null>(null);
  const [error, setError] = useState(false);
  const [busy, setBusy] = useState(false);
  const [reload, setReload] = useState(0);
  const supported = provider === "feishu" || provider === "notion" || provider === "googledrive";

  useEffect(() => {
    let disposed = false;
    setConnection(null);
    setError(false);
    if (!supported || !connectionId || connectionId.length > 256) {
      setError(true);
      return;
    }
    dataSourceCloudOauthApi.getConnectionApiAuthserviceV1CloudConnectionsConnectionIdGet({ connectionId })
      .then((response) => {
        const item = unwrapApiData<CloudConnectionResponse>(response.data);
        if (!item || item.connection_id !== connectionId || item.provider !== provider) {
          throw new Error("connection mismatch");
        }
        if (!disposed) setConnection(item);
      })
      .catch(() => { if (!disposed) setError(true); });
    return () => { disposed = true; };
  }, [provider, connectionId, supported, reload]);

  const recover = async (authorize: boolean) => {
    if (!supported || !connection || busy) return;
    setBusy(true);
    setError(false);
    try {
      if (authorize) {
        const url = await requestCloudDataSourceAuthorizeUrl(provider, {
          tenantId: "", scopes: [], reauthorizeConnectionId: connection.connection_id,
          returnUrl: window.location.href,
        });
        window.location.assign(url);
      } else {
        await enableCloudConnectionForChat(connection.connection_id);
        setReload((value) => value + 1);
      }
    } catch {
      setError(true);
    } finally {
      setBusy(false);
    }
  };

  return <Alert
    showIcon
    type={error ? "warning" : "info"}
    message={t("modelProvider.cloudDocuments.recoveryTitle")}
    description={<Space direction="vertical">
      <span>{t(error ? "modelProvider.cloudDocuments.recoveryUnavailable" : "modelProvider.cloudDocuments.recoveryHint")}</span>
      {connection && <span>{connection.display_name || connection.connection_id} · {connection.status}</span>}
      <Space wrap>
        {connection?.auth_mode === "oauth_user" && <Button loading={busy} onClick={() => void recover(true)}>{t("modelProvider.cloudDocuments.recoveryAuthorize")}</Button>}
        {connection && connection.provider_options?.chat_enabled !== true && <Button loading={busy} onClick={() => void recover(false)}>{t("modelProvider.cloudDocuments.recoveryEnable")}</Button>}
        <Button disabled={busy} onClick={() => setReload((value) => value + 1)}>{t("modelProvider.cloudDocuments.recoveryRefresh")}</Button>
      </Space>
    </Space>}
  />;
}

function hasSeenCloudDocumentsOnboarding() {
  try {
    return window.localStorage.getItem(CLOUD_DOCUMENTS_ONBOARDING_KEY) === "seen";
  } catch {
    return false;
  }
}

function rememberCloudDocumentsOnboarding() {
  try {
    window.localStorage.setItem(CLOUD_DOCUMENTS_ONBOARDING_KEY, "seen");
  } catch {
    // The guide remains available from the page header when storage is blocked.
  }
}

function GuideProviderLogo({ type }: { type: CloudProviderType }) {
  if (type === "local") {
    return <FolderOpenOutlined aria-hidden="true" />;
  }
  if (type === "googledrive") {
    return <GoogleOutlined aria-hidden="true" />;
  }
  const provider = cloudProviderOptions.find((item) => item.type === type);
  return provider?.logoUrl ? (
    <img src={provider.logoUrl} alt="" aria-hidden="true" />
  ) : (
    provider?.icon
  );
}

export default function CloudDocumentsPage() {
  const { t } = useTranslation();
  const vm = useCloudDocumentProviders();
  const [searchParams] = useSearchParams();
  const recoveryConnection = searchParams.get("connection_id") || "";
  const [guideOpen, setGuideOpen] = useState(false);
  const [guideStage, setGuideStage] = useState<GuideStage>("roadmap");
  const [guideProvider, setGuideProvider] =
    useState<CloudDocumentGuideProvider>("feishu");
  const guideInitializedRef = useRef(false);
  const providerTotal =
    cloudAuthProviderOptions.length + (vm.canCreateLocalSource ? 1 : 0) + 1;
  const providerReadyCount =
    (vm.canCreateLocalSource && vm.localSourceCount > 0 ? 1 : 0) +
    (vm.isFeishuAuthValid ? 1 : 0) +
    (vm.isNotionAuthValid ? 1 : 0) +
    (vm.isGitHubAuthValid ? 1 : 0) +
    (vm.isGoogleDriveAuthValid ? 1 : 0) +
    (vm.isWeChatOfficialAccountAuthValid ? 1 : 0) +
    (vm.isMailAuthValid ? 1 : 0);
  const hasConnectedProvider = providerReadyCount > 0;
  const hasKnowledgeSyncProvider =
    (vm.canCreateLocalSource && vm.localSourceCount > 0) ||
    vm.isFeishuAuthValid ||
    vm.isNotionAuthValid;
  const chatOnly = hasConnectedProvider && !hasKnowledgeSyncProvider;

  useEffect(() => {
    if (vm.loading || guideInitializedRef.current) {
      return;
    }
    guideInitializedRef.current = true;
    const connectedProvider = consumeCloudDocumentConnectionSuccess();
    if (connectedProvider && !recoveryConnection) {
      setGuideProvider(connectedProvider);
      setGuideStage("success");
      setGuideOpen(true);
      return;
    }
    if (!recoveryConnection && !hasConnectedProvider && !hasSeenCloudDocumentsOnboarding()) {
      setGuideStage("roadmap");
      setGuideOpen(true);
    }
  }, [vm.loading, hasConnectedProvider, recoveryConnection]);

  useEffect(() => {
    const handleConnectionSuccess = (event: Event) => {
      const provider = (event as CustomEvent<CloudDocumentGuideProvider>).detail;
      if (!provider || recoveryConnection) {
        return;
      }
      consumeCloudDocumentConnectionSuccess();
      setGuideProvider(provider);
      setGuideStage("success");
      setGuideOpen(true);
    };
    window.addEventListener(
      CLOUD_DOCUMENT_CONNECTION_SUCCESS_EVENT,
      handleConnectionSuccess,
    );
    return () => {
      window.removeEventListener(
        CLOUD_DOCUMENT_CONNECTION_SUCCESS_EVENT,
        handleConnectionSuccess,
      );
    };
  }, [recoveryConnection]);

  const openGuide = (stage: GuideStage = "roadmap") => {
    setGuideStage(stage);
    setGuideOpen(true);
  };

  const closeGuide = () => {
    rememberCloudDocumentsOnboarding();
    setGuideOpen(false);
  };

  const selectGuideProvider = (provider: CloudDocumentGuideProvider) => {
    setGuideProvider(provider);
    const action = () => {
      if (provider === "local") {
        vm.handleManageLocalSource();
      } else if (provider === "feishu") {
        vm.handleManageFeishuAuth();
      } else if (provider === "notion") {
        vm.handleOpenNotionSetup();
      } else if (provider === "wechat") {
        vm.handleManageWeChatOfficialAccount();
      } else if (provider === "github") {
        vm.handleOpenGitHubSetup();
      } else {
        vm.handleManageGoogleDrive();
      }
    };
    closeGuide();
    window.setTimeout(action, 220);
  };

  const successKnowledgePath =
    guideProvider === "googledrive" || guideProvider === "github" || guideProvider === "wechat"
      ? null
      : getCloudKnowledgeCreatePath(guideProvider);

  return (
    <div className="model-provider-page-content model-provider-service-page model-provider-cloud-doc-hub">
      <header className="model-provider-cloud-doc-page-header">
        <div className="model-provider-cloud-doc-page-heading">
          <h1>{t("modelProvider.cloudDocuments.title")}</h1>
          <p>{t("modelProvider.cloudDocuments.subtitle")}</p>
        </div>
        <div className="model-provider-cloud-doc-header-actions">
          <Button
            className="model-provider-cloud-doc-guide-entry"
            icon={<span aria-hidden="true">?</span>}
            onClick={() => openGuide("roadmap")}
          >
            {t("modelProvider.cloudDocuments.onboardingEntry")}
          </Button>
          <div
            className="model-provider-cloud-doc-overview"
            aria-label={t("modelProvider.cloudDocuments.connectedProviderSummary", {
              connected: providerReadyCount,
              total: providerTotal,
            })}
          >
            <span>{t("modelProvider.cloudDocuments.overview")}</span>
            {vm.loading ? (
              <Skeleton.Button active className="model-provider-cloud-doc-overview-skeleton" />
            ) : (
              <strong>{providerReadyCount} / {providerTotal}</strong>
            )}
            <small>{t("modelProvider.cloudDocuments.providerReadySuffix")}</small>
          </div>
        </div>
      </header>

      <section
        className="model-provider-cloud-doc-scenarios"
        aria-label={t("modelProvider.cloudDocuments.useCasesAria")}
      >
        <article className="model-provider-cloud-doc-scenario-card">
          <div className="model-provider-cloud-doc-scenario-copy">
            <h2>{t("modelProvider.cloudDocuments.chatUseCaseTitle")}</h2>
            <div className="model-provider-cloud-doc-scenario-description">
              <p>{t("modelProvider.cloudDocuments.chatUseCaseDescription")}</p>
              <Tooltip title={t("modelProvider.cloudDocuments.chatUseCaseCapabilityNote")}>
                <button
                  type="button"
                  className="model-provider-cloud-doc-capability-help"
                  aria-label={t("modelProvider.cloudDocuments.chatCapabilityHelp")}
                >
                  <QuestionOutlined aria-hidden="true" />
                </button>
              </Tooltip>
            </div>
          </div>
          <Link className="model-provider-cloud-doc-scenario-action" to={CHAT_PATH}>
            {t("modelProvider.cloudDocuments.goChat")}
          </Link>
        </article>
        <article className="model-provider-cloud-doc-scenario-card">
          <div className="model-provider-cloud-doc-scenario-copy">
            <h2>{t("modelProvider.cloudDocuments.knowledgeUseCaseTitle")}</h2>
            <p>{t("modelProvider.cloudDocuments.knowledgeUseCaseDescription")}</p>
          </div>
          <Link
            className="model-provider-cloud-doc-scenario-action"
            to={getCloudKnowledgeCreatePath()}
          >
            {t("modelProvider.cloudDocuments.goKnowledge")}
          </Link>
        </article>
      </section>

      {recoveryConnection && <ConnectionRecovery
        key={`${searchParams.get("provider")}:${recoveryConnection}`}
        provider={searchParams.get("provider") || ""}
        connectionId={recoveryConnection}
      />}
      <CloudDocumentProviderPanel vm={vm} />

      <Modal
        className="model-provider-cloud-doc-guide-modal"
        title={
          guideStage === "success"
            ? t("modelProvider.cloudDocuments.connectionSuccessTitle")
            : guideStage === "sourceChoice"
              ? t("modelProvider.cloudDocuments.sourceChoiceTitle")
              : t("modelProvider.cloudDocuments.onboardingTitle")
        }
        open={guideOpen}
        width={560}
        footer={null}
        onCancel={closeGuide}
      >
        {guideStage === "roadmap" ? (
          <div className="model-provider-cloud-doc-guide-body">
            <span className="model-provider-cloud-doc-guide-step">
              {t("modelProvider.cloudDocuments.onboardingStepBadge")}
            </span>
            <h3>{t("modelProvider.cloudDocuments.onboardingHeading")}</h3>
            <ol className="model-provider-cloud-doc-guide-roadmap">
              <li className={hasConnectedProvider ? "is-complete" : "is-current"}>
                <span className="model-provider-cloud-doc-guide-marker" aria-hidden="true">
                  {hasConnectedProvider ? <CheckOutlined /> : "1"}
                </span>
                <div>
                  <h4>{t("modelProvider.cloudDocuments.onboardingConnectTitle")}</h4>
                  <p>{t("modelProvider.cloudDocuments.onboardingConnectDescription")}</p>
                </div>
                <span className="model-provider-cloud-doc-guide-state">
                  {hasConnectedProvider
                    ? t("modelProvider.cloudDocuments.onboardingComplete")
                    : t("modelProvider.cloudDocuments.onboardingCurrent")}
                </span>
              </li>
              <li
                className={
                  hasKnowledgeSyncProvider
                    ? "is-unlocked"
                    : chatOnly
                      ? "is-partial"
                      : ""
                }
              >
                <span className="model-provider-cloud-doc-guide-marker" aria-hidden="true">
                  {hasConnectedProvider ? <CheckOutlined /> : "2"}
                </span>
                <div>
                  <h4>{t("modelProvider.cloudDocuments.onboardingUseTitle")}</h4>
                  <p>{t("modelProvider.cloudDocuments.onboardingUseDescription")}</p>
                  <div className="model-provider-cloud-doc-guide-capabilities">
                    {hasConnectedProvider ? (
                      <Link to={CHAT_PATH} onClick={closeGuide}>
                        <MessageOutlined aria-hidden="true" />
                        {t("modelProvider.cloudDocuments.guideChatCapability")}
                      </Link>
                    ) : (
                      <button type="button" disabled>
                        <MessageOutlined aria-hidden="true" />
                        {t("modelProvider.cloudDocuments.guideChatCapability")}
                      </button>
                    )}
                    {hasKnowledgeSyncProvider ? (
                      <Link to={getCloudKnowledgeCreatePath()} onClick={closeGuide}>
                        <FolderOpenOutlined aria-hidden="true" />
                        {t("modelProvider.cloudDocuments.guideKnowledgeCapability")}
                      </Link>
                    ) : (
                      <button
                        type="button"
                        disabled
                        className={chatOnly ? "is-unavailable" : ""}
                      >
                        <FolderOpenOutlined aria-hidden="true" />
                        {chatOnly
                          ? t("modelProvider.cloudDocuments.guideKnowledgeUnavailable")
                          : t("modelProvider.cloudDocuments.guideKnowledgeCapability")}
                      </button>
                    )}
                  </div>
                </div>
                <span className="model-provider-cloud-doc-guide-state">
                  {chatOnly
                    ? t("modelProvider.cloudDocuments.onboardingPartiallyUnlocked")
                    : hasConnectedProvider
                      ? t("modelProvider.cloudDocuments.onboardingUnlocked")
                      : t("modelProvider.cloudDocuments.onboardingLocked")}
                </span>
              </li>
            </ol>
            <div className="model-provider-cloud-doc-guide-actions">
              <Button onClick={closeGuide}>
                {t("modelProvider.cloudDocuments.onboardingLater")}
              </Button>
              <Button type="primary" onClick={() => setGuideStage("sourceChoice")}>
                {hasConnectedProvider
                  ? t("modelProvider.cloudDocuments.connectAnotherSource")
                  : t("modelProvider.cloudDocuments.onboardingPrimaryAction")}
              </Button>
            </div>
          </div>
        ) : null}

        {guideStage === "sourceChoice" ? (
          <div className="model-provider-cloud-doc-guide-body">
            <span className="model-provider-cloud-doc-guide-step">
              {t("modelProvider.cloudDocuments.sourceChoiceStep")}
            </span>
            <h3>{t("modelProvider.cloudDocuments.sourceChoiceHeading")}</h3>
            <p className="model-provider-cloud-doc-guide-description">
              {t("modelProvider.cloudDocuments.sourceChoiceDescription")}
            </p>
            <div className="model-provider-cloud-doc-source-choice-grid">
              {cloudProviderOptions
                .filter((provider) => provider.type !== "local" || vm.canCreateLocalSource)
                .map((provider) => (
                  <button
                    key={provider.type}
                    type="button"
                    onClick={() => selectGuideProvider(provider.type)}
                  >
                    <span className="model-provider-cloud-doc-source-choice-logo">
                      <GuideProviderLogo type={provider.type} />
                    </span>
                    <span>
                      <strong>{t(`modelProvider.cloudDocuments.guideSource.${provider.type}.title`)}</strong>
                      <small>{t(`modelProvider.cloudDocuments.guideSource.${provider.type}.description`)}</small>
                    </span>
                    <ArrowRightOutlined aria-hidden="true" />
                  </button>
                ))}
            </div>
            <div className="model-provider-cloud-doc-guide-actions">
              <Button onClick={() => setGuideStage("roadmap")}>
                {t("modelProvider.cloudDocuments.sourceChoicePrevious")}
              </Button>
              <Button onClick={closeGuide}>
                {t("modelProvider.cloudDocuments.onboardingLater")}
              </Button>
            </div>
          </div>
        ) : null}

        {guideStage === "success" ? (
          <div className="model-provider-cloud-doc-guide-body model-provider-cloud-doc-guide-success">
            <span className="model-provider-cloud-doc-guide-success-icon" aria-hidden="true">
              <CheckOutlined />
            </span>
            <h3>{t("modelProvider.cloudDocuments.connectionSuccessHeading")}</h3>
            <p className="model-provider-cloud-doc-guide-description">
              {guideProvider === "googledrive"
                ? t("modelProvider.cloudDocuments.connectionSuccessGoogleDescription")
                : guideProvider === "wechat"
                  ? t("modelProvider.cloudDocuments.connectionSuccessWeChatDescription")
                  : guideProvider === "github"
                    ? t("modelProvider.cloudDocuments.connectionSuccessGitHubDescription")
                    : t("modelProvider.cloudDocuments.connectionSuccessDescription")}
            </p>
            <div className="model-provider-cloud-doc-guide-next-list">
              <Link to={CHAT_PATH} onClick={closeGuide}>
                <span><MessageOutlined aria-hidden="true" /></span>
                <span>
                  <strong>
                    {t(`modelProvider.cloudDocuments.successChat.${guideProvider}.title`)}
                  </strong>
                  <small>
                    {t(`modelProvider.cloudDocuments.successChat.${guideProvider}.description`)}
                  </small>
                </span>
                <ArrowRightOutlined aria-hidden="true" />
              </Link>
              {successKnowledgePath ? (
                <Link to={successKnowledgePath} onClick={closeGuide}>
                  <span><FolderOpenOutlined aria-hidden="true" /></span>
                  <span>
                    <strong>{t("modelProvider.cloudDocuments.successKnowledgeTitle")}</strong>
                    <small>
                      {t(
                        `modelProvider.cloudDocuments.successKnowledge.${guideProvider}.description`,
                      )}
                    </small>
                  </span>
                  <ArrowRightOutlined aria-hidden="true" />
                </Link>
              ) : null}
            </div>
            <div className="model-provider-cloud-doc-guide-actions">
              <Button onClick={closeGuide}>
                {t("modelProvider.cloudDocuments.onboardingGotIt")}
              </Button>
            </div>
          </div>
        ) : null}
      </Modal>

      <CloudDocumentModals vm={vm} />
    </div>
  );
}
