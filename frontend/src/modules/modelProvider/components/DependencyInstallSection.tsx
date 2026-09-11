import { useCallback, useEffect, useMemo, useState, type ChangeEvent } from "react";
import {
  Alert,
  Button,
  Empty,
  Input,
  Modal,
  Popconfirm,
  Segmented,
  Space,
  Spin,
  Tag,
  Tooltip,
  message,
} from "antd";
import {
  DownloadOutlined,
  CopyOutlined,
  DeleteOutlined,
  FolderOpenOutlined,
  GlobalOutlined,
  RightOutlined,
  SearchOutlined,
  SettingOutlined,
  SyncOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { useLocation } from "react-router-dom";
import {
  checkFFmpegDependency,
  checkEditablePPTDependency,
  checkBrowserExtensionDependency,
  createBrowserPairingCode,
  getBrowserDevices,
  getFFmpegDependencyStatus,
  getEditablePPTDependencyStatus,
  getBrowserExtensionDependencyStatus,
  installFFmpegDependency,
  installEditablePPTDependency,
  installBrowserExtensionDependency,
  revokeBrowserDevice,
  updateFFmpegDependency,
  type EditablePPTDependencyStatus,
  type BrowserExtensionDependencyStatus,
  type BrowserDeviceInfo,
  type BrowserPairingCode,
  type FFmpegDependencyStatus,
} from "../api/systemDependencies";
import { getLocalizedErrorMessage } from "@/components/request";
import { isDesktopRuntime, isLocalRuntime } from "@/runtime/mode";
import { openBrowserExtensionDir, selectExecutable } from "@/runtime/desktopBridge";
import {
  detectPreferredBrowserExtensionTarget,
  resolveBrowserExtensionTargets,
  type BrowserExtensionTargetID,
} from "../utils/browserExtensionTarget";


const DEPENDENCY_ICON_DATA_URL =
  "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAACAAAAAgCAYAAABzenr0AAAC50lEQVR4Ab3XAWQbURjA8UxUTVRVVc3URAxUVQ0xw1A1lUxVTUVNTRXD1ExNMUVRRcwwU0UBM1ynqmaoICZmZqIqqioiIiIOERMR8fa/+nCk70muZ/jty/Luvu97L/dek4BS6tb/FD+I9yGEQdy+cQMxKxaUhCMYwwTvTRMX41b8FbZ4vQsLaZyihAZS1zbApf0kGeaCexjHYyzw/kviCnGQcWcmH5BFHjZaUD34GuCfZUn0GSckzxILqKCONpQbxbPEPdeYF98wEJBlUV2zrooWb1g8hWFntXtuAGU0PRW2ruIPDDnFvTRQQUMz1o4dxIzPAH7hjlPY3cAqjlGGMrBR0xXHDo4Nsz8jjkphAAHX/nxnKF4jgW0Y/4Rtw3ORQ5gVmiVPktfBjgYY/K65+S8qhuJfsG7YgnlyR4hRdo9NLGGg2wZavG8qfoJVQ/ECxhGFDdVbA2Y/sWQoXnYVr0L52cApn2VC91DKqkUxhSKUnw3kkTA8FzU8pMFJYgHKzwYqSJC8YCg+w8N23zVz3xqoI4GcZryBJwjjAsq3BhhrEF/gt26rcs0CMUKU4v410MQaUoaZL2IMZ1B+NtDGJixdc4ysSHFZHf8aaCOJfd35L19Q7uKP5pqWtwYsUJj4XnO+t/EGQ8ho/viUyLnpdQUO0Xkz2GJt4gaGkdYtM9dNEOe7bkC+kqWxjdeGmW9hBGntzCkuOVd7aaBP4jPD+Z7EEI4040VMyorO8drWNBDqaEBummWwrkmexbyh+CWmZBJzmjwtbCB43Qo8QhXKg3Pn+JXfCU+dg0lzXYaP6DkxhmmEA1J8gtkXPRa/oGhE8iyjAdUN7tsNyPF56aW4HLthV/Fmj9+S95wGPiKHImw0uujciVlEEMSK+T59A+5nIIRRRBiYIs5giVmu8f9t+SV0iDTeYlAe3DX3rvHcgFdyFD8g2SxxGevYwT6OkME5qmjB3cC6pgH/yEfUj5D8qA0To3B2y8g/W3EFMEBZY/QAAAAASUVORK5CYII=";

export default function DependencyInstallSection() {
  const { t } = useTranslation();
  const location = useLocation();
  const showSection = isLocalRuntime() || isDesktopRuntime();
  const [loading, setLoading] = useState(false);
  const [status, setStatus] = useState<FFmpegDependencyStatus | null>(null);
  const [pptStatus, setPptStatus] = useState<EditablePPTDependencyStatus | null>(null);
  const [browserStatus, setBrowserStatus] = useState<BrowserExtensionDependencyStatus | null>(null);
  const [loadError, setLoadError] = useState("");
  const [modalOpen, setModalOpen] = useState(false);
  const [pptModalOpen, setPptModalOpen] = useState(false);
  const [browserModalOpen, setBrowserModalOpen] = useState(false);
  const [customPath, setCustomPath] = useState("");
  const [saving, setSaving] = useState(false);
  const [installing, setInstalling] = useState(false);
  const [checking, setChecking] = useState(false);
  const [pptInstalling, setPptInstalling] = useState(false);
  const [pptChecking, setPptChecking] = useState(false);
  const [browserInstalling, setBrowserInstalling] = useState(false);
  const [browserChecking, setBrowserChecking] = useState(false);
  const [browserPairing, setBrowserPairing] = useState<BrowserPairingCode | null>(null);
  const [browserPairingCreating, setBrowserPairingCreating] = useState(false);
  const [browserDevices, setBrowserDevices] = useState<BrowserDeviceInfo[]>([]);
  const [browserDevicesLoading, setBrowserDevicesLoading] = useState(false);
  const [browserDevicesError, setBrowserDevicesError] = useState("");
  const [browserDeviceRevoking, setBrowserDeviceRevoking] = useState("");
  const [browserTargetID, setBrowserTargetID] = useState<BrowserExtensionTargetID>(
    detectPreferredBrowserExtensionTarget,
  );
  const [searchValue, setSearchValue] = useState("");

  const browserTargets = useMemo(
    () => resolveBrowserExtensionTargets(browserStatus?.supportedBrowsers),
    [browserStatus?.supportedBrowsers],
  );
  const browserTarget = browserTargets.find((target) => target.id === browserTargetID)
    || browserTargets[0];

  const refresh = useCallback(async () => {
    setLoading(true);
    setLoadError("");
    try {
      const [next, nextPpt, nextBrowser] = await Promise.all([
        getFFmpegDependencyStatus(),
        getEditablePPTDependencyStatus(),
        getBrowserExtensionDependencyStatus(),
      ]);
      setStatus(next);
      setPptStatus(nextPpt);
      setBrowserStatus(nextBrowser);
      setCustomPath(next.customPath || "");
    } catch (error) {
      setLoadError(getLocalizedErrorMessage(error) || t("modelProvider.external.dependencyLoadFailed"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    if (showSection) {
      void refresh();
    }
  }, [refresh, showSection]);

  useEffect(() => {
    if (!showSection) {
      return;
    }
    const dependencyId = location.hash === "#editable-ppt-dependency"
      ? "editable-ppt-dependency"
      : location.hash === "#browser-extension-dependency"
        ? "browser-extension-dependency"
        : location.hash === "#ffmpeg-dependency"
          ? "ffmpeg-dependency"
          : "";
    if (!dependencyId) return;
    if (dependencyId === "editable-ppt-dependency") setPptModalOpen(true);
    else if (dependencyId === "browser-extension-dependency") setBrowserModalOpen(true);
    else setModalOpen(true);
    requestAnimationFrame(() => {
      document
        .getElementById(dependencyId)
        ?.scrollIntoView({ behavior: "smooth", block: "center" });
    });
  }, [location.hash, showSection]);

  const cardStatus = useMemo(() => {
    if (!status) {
      return "missing" as const;
    }
    return status.installed ? ("configured" as const) : ("missing" as const);
  }, [status]);
  const normalizedSearchValue = searchValue.trim().toLowerCase();
  const shouldShowFFmpegCard = useMemo(() => {
    if (!normalizedSearchValue) {
      return true;
    }
    const title = t("modelProvider.external.dependencyFfmpegTitle").toLowerCase();
    const summary = t("modelProvider.external.dependencyFfmpegSummary").toLowerCase();
    return title.includes(normalizedSearchValue) || summary.includes(normalizedSearchValue);
  }, [normalizedSearchValue, t]);
  const shouldShowPptCard = useMemo(() => {
    if (!normalizedSearchValue) return true;
    return t("modelProvider.external.dependencyEditablePptTitle").toLowerCase().includes(normalizedSearchValue)
      || t("modelProvider.external.dependencyEditablePptSummary").toLowerCase().includes(normalizedSearchValue);
  }, [normalizedSearchValue, t]);
  const shouldShowBrowserCard = useMemo(() => {
    if (!normalizedSearchValue) return true;
    return t("modelProvider.external.dependencyBrowserExtensionTitle").toLowerCase().includes(normalizedSearchValue)
      || t("modelProvider.external.dependencyBrowserExtensionSummary").toLowerCase().includes(normalizedSearchValue);
  }, [normalizedSearchValue, t]);

  const handleSaveCustomPath = async () => {
    const trimmed = customPath.trim();
    if (!trimmed) {
      message.warning(t("modelProvider.external.dependencyCustomPathRequired"));
      return;
    }
    setSaving(true);
    try {
      const next = await updateFFmpegDependency({ source: "custom", customPath: trimmed });
      setStatus(next);
      setModalOpen(false);
      message.success(t("modelProvider.external.dependencySaved"));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error) || t("modelProvider.external.dependencySaveFailed"));
    } finally {
      setSaving(false);
    }
  };

  const handleInstallBundled = async () => {
    setInstalling(true);
    try {
      const next = await installFFmpegDependency();
      setStatus(next);
      message.success(t("modelProvider.external.dependencyInstallSuccess"));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error) || t("modelProvider.external.dependencyInstallFailed"));
    } finally {
      setInstalling(false);
    }
  };

  const handleRecheck = async () => {
    setChecking(true);
    try {
      const next = await checkFFmpegDependency();
      setStatus(next);
      message.success(
        next.installed
          ? t("modelProvider.external.dependencyCheckReady")
          : t("modelProvider.external.dependencyCheckMissing"),
      );
    } catch (error) {
      message.error(getLocalizedErrorMessage(error) || t("modelProvider.external.dependencyLoadFailed"));
    } finally {
      setChecking(false);
    }
  };

  const handleBrowseExecutable = async () => {
    const selected = await selectExecutable();
    if (selected) {
      setCustomPath(selected);
    }
  };

  const handleInstallEditablePPT = async () => {
    setPptInstalling(true);
    try {
      const next = await installEditablePPTDependency();
      setPptStatus(next);
      message.success(t("modelProvider.external.dependencyEditablePptInstallSuccess"));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error) || t("modelProvider.external.dependencyEditablePptInstallFailed"));
    } finally {
      setPptInstalling(false);
    }
  };

  const handleRecheckEditablePPT = async () => {
    setPptChecking(true);
    try {
      const next = await checkEditablePPTDependency();
      setPptStatus(next);
      message.success(next.installed
        ? t("modelProvider.external.dependencyCheckReady")
        : t("modelProvider.external.dependencyCheckMissing"));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error) || t("modelProvider.external.dependencyLoadFailed"));
    } finally {
      setPptChecking(false);
    }
  };

  const handleInstallBrowserExtension = async () => {
    setBrowserInstalling(true);
    try {
      const next = await installBrowserExtensionDependency();
      setBrowserStatus(next);
      message.success(t("modelProvider.external.dependencyBrowserExtensionInstallSuccess"));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error) || t("modelProvider.external.dependencyBrowserExtensionInstallFailed"));
    } finally {
      setBrowserInstalling(false);
    }
  };

  const handleRecheckBrowserExtension = async () => {
    setBrowserChecking(true);
    try {
      const next = await checkBrowserExtensionDependency();
      setBrowserStatus(next);
      message.success(next.installed
        ? t("modelProvider.external.dependencyCheckReady")
        : t("modelProvider.external.dependencyCheckMissing"));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error) || t("modelProvider.external.dependencyLoadFailed"));
    } finally {
      setBrowserChecking(false);
    }
  };

  const handleRevealBrowserExtension = async () => {
    const result = await openBrowserExtensionDir();
    if (!result.ok) {
      message.error(t("modelProvider.external.dependencyBrowserExtensionRevealFailed"));
    }
  };

  const handleCreateBrowserPairing = async () => {
    setBrowserPairingCreating(true);
    try {
      const pairing = await createBrowserPairingCode();
      setBrowserPairing(pairing);
      message.success(t("modelProvider.external.dependencyBrowserPairingCreated"));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error) || t("modelProvider.external.dependencyBrowserPairingFailed"));
    } finally {
      setBrowserPairingCreating(false);
    }
  };

  const handleCopyBrowserPairing = async () => {
    if (!browserPairing?.code) return;
    try {
      await navigator.clipboard.writeText(browserPairing.code);
      message.success(t("modelProvider.external.dependencyBrowserPairingCopied"));
    } catch {
      message.error(t("modelProvider.external.dependencyBrowserPairingCopyFailed"));
    }
  };

  const handleCopyBrowserSettingsURL = async () => {
    if (!browserTarget?.settingsUrl) return;
    try {
      await navigator.clipboard.writeText(browserTarget.settingsUrl);
      message.success(t("modelProvider.external.dependencyBrowserSettingsCopied", {
        browser: browserTarget.name,
      }));
    } catch {
      message.error(t("modelProvider.external.dependencyBrowserSettingsCopyFailed"));
    }
  };

  const refreshBrowserDevices = useCallback(async () => {
    setBrowserDevicesLoading(true);
    setBrowserDevicesError("");
    try {
      setBrowserDevices(await getBrowserDevices());
    } catch (error) {
      setBrowserDevicesError(
        getLocalizedErrorMessage(error) || t("modelProvider.external.dependencyBrowserDevicesLoadFailed"),
      );
    } finally {
      setBrowserDevicesLoading(false);
    }
  }, [t]);

  useEffect(() => {
    if (browserModalOpen) void refreshBrowserDevices();
  }, [browserModalOpen, refreshBrowserDevices]);

  const handleRevokeBrowserDevice = async (deviceID: string) => {
    setBrowserDeviceRevoking(deviceID);
    try {
      await revokeBrowserDevice(deviceID);
      setBrowserDevices((devices) => devices.filter((device) => device.id !== deviceID));
      message.success(t("modelProvider.external.dependencyBrowserDeviceRevoked"));
    } catch (error) {
      message.error(
        getLocalizedErrorMessage(error) || t("modelProvider.external.dependencyBrowserDeviceRevokeFailed"),
      );
    } finally {
      setBrowserDeviceRevoking("");
    }
  };

  if (!showSection) {
    return null;
  }

  return (
    <>
      <section
        className="model-provider-service-category"
        id="ffmpeg-dependency"
      >
        <div className="model-provider-service-category-top">
          <div className="model-provider-service-category-head">
            <span aria-hidden="true">
              <SettingOutlined />
            </span>
            <div>
              <h3>{t("modelProvider.external.dependencyCategoryTitle")}</h3>
              <p>{t("modelProvider.external.dependencyCategoryDesc")}</p>
            </div>
          </div>
          <Input
            allowClear
            className="model-provider-category-search"
            onChange={(event: ChangeEvent<HTMLInputElement>) => setSearchValue(event.target.value)}
            placeholder={t("modelProvider.external.searchPlaceholder")}
            prefix={<SearchOutlined />}
            value={searchValue}
          />
        </div>

        {loadError ? (
          <Alert
            action={
              <Button size="small" type="primary" onClick={() => void refresh()}>
                {t("common.retry")}
              </Button>
            }
            message={loadError}
            showIcon
            type="error"
          />
        ) : null}

        <Spin spinning={loading && !status}>
          <div className="model-provider-service-grid">
            {shouldShowFFmpegCard ? (
              <button
                className="model-provider-service-card tone-violet"
                onClick={() => setModalOpen(true)}
                type="button"
              >
                <span className="model-provider-service-logo" aria-hidden="true">
                  <img
                    alt=""
                    className="model-provider-dependency-inline-icon"
                    src={DEPENDENCY_ICON_DATA_URL}
                  />
                </span>
                <div className="model-provider-service-card-copy">
                  <div className="model-provider-service-title-row">
                    <h4>{t("modelProvider.external.dependencyFfmpegTitle")}</h4>
                    <Tag
                      className="model-provider-service-status"
                      color={cardStatus === "configured" ? "success" : "default"}
                    >
                      {t(`modelProvider.external.status.${cardStatus}`)}
                    </Tag>
                  </div>
                  <Tooltip placement="topLeft" title={t("modelProvider.external.dependencyFfmpegSummary")}>
                    <span className="model-provider-service-summary-wrap">
                      <p className="model-provider-service-summary">{t("modelProvider.external.dependencyFfmpegSummary")}</p>
                    </span>
                  </Tooltip>
                </div>
                <span className="model-provider-service-card-arrow" aria-hidden="true">
                  <RightOutlined />
                </span>
              </button>
            ) : null}
            {shouldShowPptCard ? (
              <button
                className="model-provider-service-card tone-blue"
                id="editable-ppt-dependency"
                onClick={() => setPptModalOpen(true)}
                type="button"
              >
                <span className="model-provider-service-logo" aria-hidden="true">
                  <SettingOutlined />
                </span>
                <div className="model-provider-service-card-copy">
                  <div className="model-provider-service-title-row">
                    <h4>{t("modelProvider.external.dependencyEditablePptTitle")}</h4>
                    <Tag className="model-provider-service-status" color={pptStatus?.installed ? "success" : "default"}>
                      {t(`modelProvider.external.status.${pptStatus?.installed ? "configured" : "missing"}`)}
                    </Tag>
                  </div>
                  <Tooltip placement="topLeft" title={t("modelProvider.external.dependencyEditablePptSummary")}>
                    <span className="model-provider-service-summary-wrap">
                      <p className="model-provider-service-summary">{t("modelProvider.external.dependencyEditablePptSummary")}</p>
                    </span>
                  </Tooltip>
                </div>
                <span className="model-provider-service-card-arrow" aria-hidden="true"><RightOutlined /></span>
              </button>
            ) : null}
            {shouldShowBrowserCard ? (
              <button
                className="model-provider-service-card tone-blue"
                id="browser-extension-dependency"
                onClick={() => setBrowserModalOpen(true)}
                type="button"
              >
                <span className="model-provider-service-logo" aria-hidden="true">
                  <GlobalOutlined />
                </span>
                <div className="model-provider-service-card-copy">
                  <div className="model-provider-service-title-row">
                    <h4>{t("modelProvider.external.dependencyBrowserExtensionTitle")}</h4>
                    <Tag className="model-provider-service-status" color={browserStatus?.installed ? "success" : "default"}>
                      {t(`modelProvider.external.status.${browserStatus?.installed ? "configured" : "missing"}`)}
                    </Tag>
                  </div>
                  <Tooltip placement="topLeft" title={t("modelProvider.external.dependencyBrowserExtensionSummary")}>
                    <span className="model-provider-service-summary-wrap">
                      <p className="model-provider-service-summary">{t("modelProvider.external.dependencyBrowserExtensionSummary")}</p>
                    </span>
                  </Tooltip>
                </div>
                <span className="model-provider-service-card-arrow" aria-hidden="true"><RightOutlined /></span>
              </button>
            ) : null}
          </div>
          {!loading && !loadError && !shouldShowFFmpegCard && !shouldShowPptCard && !shouldShowBrowserCard ? (
            <div className="model-provider-category-empty">
              <Empty description={t("modelProvider.external.noMatchedServices")} image={Empty.PRESENTED_IMAGE_SIMPLE} />
            </div>
          ) : null}
        </Spin>
      </section>

      <Modal
        className="model-provider-service-config-modal"
        destroyOnClose
        footer={null}
        onCancel={() => setModalOpen(false)}
        open={modalOpen}
        title={t("modelProvider.external.dependencyFfmpegModalTitle")}
        width={640}
      >
        <Space direction="vertical" size={16} style={{ width: "100%" }}>
          <Alert
            message={t("modelProvider.external.dependencyFfmpegImpact")}
            showIcon
            type="info"
          />
          {status?.message && !status.installed ? (
            <Alert message={status.message} showIcon type="warning" />
          ) : null}
          {status?.installed ? (
            <Alert
              message={t("modelProvider.external.dependencyDetected", {
                ffmpeg: status.ffmpegPath || "-",
                ffprobe: status.ffprobePath || "-",
              })}
              showIcon
              type="success"
            />
          ) : null}

          <div>
            <div className="model-provider-service-title-row">
              <h4>{t("modelProvider.external.dependencyInstallBundledTitle")}</h4>
            </div>
            <p>{t("modelProvider.external.dependencyInstallBundledDesc")}</p>
            <Button
              disabled={!status?.installSupported || Boolean(status?.installed)}
              icon={<DownloadOutlined />}
              loading={installing}
              onClick={() => void handleInstallBundled()}
              type="primary"
            >
              {status?.installed
                ? t("modelProvider.external.dependencyInstalledAction")
                : t("modelProvider.external.dependencyInstallAction")}
            </Button>
          </div>

          <div>
            <div className="model-provider-service-title-row">
              <h4>{t("modelProvider.external.dependencyCustomPathTitle")}</h4>
            </div>
            <p>{t("modelProvider.external.dependencyCustomPathDesc")}</p>
            <Space.Compact style={{ width: "100%" }}>
              <Input
                onChange={(event: ChangeEvent<HTMLInputElement>) => setCustomPath(event.target.value)}
                placeholder={t("modelProvider.external.dependencyCustomPathPlaceholder")}
                value={customPath}
              />
              {isDesktopRuntime() ? (
                <Button icon={<FolderOpenOutlined />} onClick={() => void handleBrowseExecutable()}>
                  {t("modelProvider.external.dependencyBrowseAction")}
                </Button>
              ) : null}
            </Space.Compact>
            <Space style={{ marginTop: 12 }}>
              <Button loading={saving} onClick={() => void handleSaveCustomPath()} type="primary">
                {t("modelProvider.external.dependencySavePathAction")}
              </Button>
            </Space>
          </div>

          <div>
            <Button icon={<SyncOutlined />} loading={checking} onClick={() => void handleRecheck()}>
              {t("modelProvider.external.dependencyRecheckAction")}
            </Button>
          </div>
        </Space>
      </Modal>

      <Modal
        className="model-provider-service-config-modal"
        destroyOnClose
        footer={null}
        onCancel={() => setPptModalOpen(false)}
        open={pptModalOpen}
        title={t("modelProvider.external.dependencyEditablePptModalTitle")}
        width={640}
      >
        <Space direction="vertical" size={16} style={{ width: "100%" }}>
          <Alert message={t("modelProvider.external.dependencyEditablePptImpact")} showIcon type="info" />
          {pptStatus?.message && !pptStatus.installed ? <Alert message={pptStatus.message} showIcon type="warning" /> : null}
          {pptStatus?.installed ? (
            <Alert
              message={t("modelProvider.external.dependencyEditablePptDetected", {
                chromium: pptStatus.chromiumPath || "-",
              })}
              showIcon
              type="success"
            />
          ) : null}
          <div>
            <h4>{t("modelProvider.external.dependencyInstallBundledTitle")}</h4>
            <p>{t("modelProvider.external.dependencyEditablePptInstallDesc")}</p>
            <Space>
              <Button
                disabled={!pptStatus?.installSupported || Boolean(pptStatus?.installed)}
                icon={<DownloadOutlined />}
                loading={pptInstalling}
                onClick={() => void handleInstallEditablePPT()}
                type="primary"
              >
                {pptStatus?.installed
                  ? t("modelProvider.external.dependencyInstalledAction")
                  : t("modelProvider.external.dependencyInstallAction")}
              </Button>
              <Button icon={<SyncOutlined />} loading={pptChecking} onClick={() => void handleRecheckEditablePPT()}>
                {t("modelProvider.external.dependencyRecheckAction")}
              </Button>
            </Space>
          </div>
        </Space>
      </Modal>

      <Modal
        className="model-provider-service-config-modal"
        destroyOnClose
        footer={null}
        onCancel={() => {
          setBrowserModalOpen(false);
          setBrowserPairing(null);
        }}
        open={browserModalOpen}
        title={t("modelProvider.external.dependencyBrowserExtensionModalTitle")}
        width={640}
      >
        <Space direction="vertical" size={16} style={{ width: "100%" }}>
          <Alert
            message={t("modelProvider.external.dependencyBrowserExtensionImpact", {
              browser: browserTarget.name,
            })}
            showIcon
            type="info"
          />
          <div>
            <h4>{t("modelProvider.external.dependencyBrowserSelectTitle")}</h4>
            <Segmented
              onChange={(value) => setBrowserTargetID(value as BrowserExtensionTargetID)}
              options={browserTargets.map((target) => ({ label: target.name, value: target.id }))}
              value={browserTarget.id}
            />
          </div>
          {browserStatus?.message && !browserStatus.installed
            ? <Alert message={browserStatus.message} showIcon type="warning" />
            : null}
          {browserStatus?.installed ? (
            <>
              <Alert
                message={t("modelProvider.external.dependencyBrowserExtensionDetected", {
                  path: browserStatus.installDir || "-",
                  version: browserStatus.version || "-",
                })}
                showIcon
                type="success"
              />
              <Alert
                message={t("modelProvider.external.dependencyBrowserExtensionApproval", {
                  browser: browserTarget.name,
                  settingsUrl: browserTarget.settingsUrl,
                })}
                showIcon
                type="warning"
              />
            </>
          ) : null}
          <div>
            <h4>{t("modelProvider.external.dependencyInstallBundledTitle")}</h4>
            <p>{t("modelProvider.external.dependencyBrowserExtensionInstallDesc")}</p>
            <Space>
              <Button
                disabled={!browserStatus?.installSupported}
                icon={<DownloadOutlined />}
                loading={browserInstalling}
                onClick={() => void handleInstallBrowserExtension()}
                type="primary"
              >
                {browserStatus?.installed
                  ? t(browserStatus.updateAvailable
                    ? "modelProvider.external.dependencyBrowserExtensionUpdateAction"
                    : "modelProvider.external.dependencyBrowserExtensionReinstallAction")
                  : t("modelProvider.external.dependencyInstallAction")}
              </Button>
              <Button
                icon={<SyncOutlined />}
                loading={browserChecking}
                onClick={() => void handleRecheckBrowserExtension()}
              >
                {t("modelProvider.external.dependencyRecheckAction")}
              </Button>
              {browserStatus?.installed && isDesktopRuntime() ? (
                <Button icon={<FolderOpenOutlined />} onClick={() => void handleRevealBrowserExtension()}>
                  {t("modelProvider.external.dependencyOpenInstallDirAction")}
                </Button>
              ) : null}
            </Space>
          </div>
          <div>
            <h4>{t("modelProvider.external.dependencyBrowserSettingsTitle", {
              browser: browserTarget.name,
            })}</h4>
            <p>{t("modelProvider.external.dependencyBrowserSettingsDesc", {
              browser: browserTarget.name,
            })}</p>
            <Space.Compact style={{ width: "100%" }}>
              <Input readOnly value={browserTarget.settingsUrl} />
              <Button icon={<CopyOutlined />} onClick={() => void handleCopyBrowserSettingsURL()}>
                {t("modelProvider.external.dependencyBrowserSettingsCopyAction")}
              </Button>
            </Space.Compact>
          </div>
          <div>
            <h4>{t("modelProvider.external.dependencyBrowserPairingTitle", {
              browser: browserTarget.name,
            })}</h4>
            <p>{t("modelProvider.external.dependencyBrowserPairingDesc")}</p>
            {browserPairing?.code ? (
              <Space.Compact style={{ width: "100%", marginBottom: 12 }}>
                <Input readOnly value={browserPairing.code} />
                <Button onClick={() => void handleCopyBrowserPairing()}>
                  {t("modelProvider.external.dependencyBrowserPairingCopyAction")}
                </Button>
              </Space.Compact>
            ) : null}
            <Button
              loading={browserPairingCreating}
              onClick={() => void handleCreateBrowserPairing()}
              type="primary"
            >
              {t("modelProvider.external.dependencyBrowserPairingCreateAction")}
            </Button>
          </div>
          <div>
            <Space align="center" style={{ display: "flex", justifyContent: "space-between" }}>
              <div>
                <h4 style={{ marginBottom: 4 }}>
                  {t("modelProvider.external.dependencyBrowserDevicesTitle")}
                </h4>
                <p style={{ marginBottom: 0 }}>
                  {t("modelProvider.external.dependencyBrowserDevicesDesc")}
                </p>
              </div>
              <Button
                icon={<SyncOutlined />}
                loading={browserDevicesLoading}
                onClick={() => void refreshBrowserDevices()}
                size="small"
              >
                {t("common.refresh")}
              </Button>
            </Space>
            {browserDevicesError ? (
              <Alert
                action={(
                  <Button onClick={() => void refreshBrowserDevices()} size="small">
                    {t("common.retry")}
                  </Button>
                )}
                message={browserDevicesError}
                showIcon
                style={{ marginTop: 12 }}
                type="error"
              />
            ) : null}
            <Spin spinning={browserDevicesLoading}>
              {!browserDevicesLoading && browserDevices.length === 0 ? (
                <Empty
                  description={t("modelProvider.external.dependencyBrowserDevicesEmpty")}
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  style={{ marginBlock: 16 }}
                />
              ) : (
                <Space direction="vertical" size={8} style={{ marginTop: 12, width: "100%" }}>
                  {browserDevices.map((device) => (
                    <div
                      key={device.id}
                      style={{
                        alignItems: "center",
                        border: "1px solid var(--ant-color-border-secondary, #f0f0f0)",
                        borderRadius: 8,
                        display: "flex",
                        gap: 12,
                        justifyContent: "space-between",
                        padding: "10px 12px",
                      }}
                    >
                      <div style={{ minWidth: 0 }}>
                        <Space size={8} wrap>
                          <strong>{device.name || device.browser || t("modelProvider.external.dependencyBrowserDeviceFallback")}</strong>
                          <Tag color={device.online ? "success" : "default"}>
                            {t(device.online
                              ? "modelProvider.external.dependencyBrowserDeviceOnline"
                              : "modelProvider.external.dependencyBrowserDeviceOffline")}
                          </Tag>
                        </Space>
                        <div style={{ color: "var(--ant-color-text-secondary, #8c8c8c)", fontSize: 12 }}>
                          {[device.browser, device.browser_version, device.extension_version && `Extension ${device.extension_version}`]
                            .filter(Boolean)
                            .join(" · ")}
                        </div>
                        <div style={{ color: "var(--ant-color-text-tertiary, #bfbfbf)", fontSize: 12 }}>
                          {t("modelProvider.external.dependencyBrowserDeviceLastSeen", {
                            time: new Date(device.last_seen_at).toLocaleString(),
                          })}
                        </div>
                      </div>
                      <Popconfirm
                        cancelText={t("common.cancel")}
                        description={t("modelProvider.external.dependencyBrowserDeviceRevokeConfirm")}
                        okButtonProps={{ danger: true }}
                        okText={t("modelProvider.external.dependencyBrowserDeviceRevokeAction")}
                        onConfirm={() => void handleRevokeBrowserDevice(device.id)}
                        title={t("modelProvider.external.dependencyBrowserDeviceRevokeTitle")}
                      >
                        <Button
                          danger
                          icon={<DeleteOutlined />}
                          loading={browserDeviceRevoking === device.id}
                          size="small"
                        >
                          {t("modelProvider.external.dependencyBrowserDeviceRevokeAction")}
                        </Button>
                      </Popconfirm>
                    </div>
                  ))}
                </Space>
              )}
            </Spin>
          </div>
        </Space>
      </Modal>
    </>
  );
}
