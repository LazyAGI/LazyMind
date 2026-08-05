import { useCallback, useEffect, useMemo, useState, type ChangeEvent } from "react";
import {
  Alert,
  Button,
  Empty,
  Input,
  Modal,
  Space,
  Spin,
  Tag,
  Tooltip,
  message,
} from "antd";
import {
  DownloadOutlined,
  FolderOpenOutlined,
  RightOutlined,
  SearchOutlined,
  SettingOutlined,
  SyncOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { useLocation } from "react-router-dom";
import {
  checkFFmpegDependency,
  getFFmpegDependencyStatus,
  installFFmpegDependency,
  updateFFmpegDependency,
  type FFmpegDependencyStatus,
} from "../api/systemDependencies";
import { getLocalizedErrorMessage } from "@/components/request";
import { isDesktopRuntime, isLocalRuntime } from "@/runtime/mode";
import { selectExecutable } from "@/runtime/desktopBridge";


const DEPENDENCY_ICON_DATA_URL =
  "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAACAAAAAgCAYAAABzenr0AAAC50lEQVR4Ab3XAWQbURjA8UxUTVRVVc3URAxUVQ0xw1A1lUxVTUVNTRXD1ExNMUVRRcwwU0UBM1ynqmaoICZmZqIqqioiIiIOERMR8fa/+nCk70muZ/jty/Luvu97L/dek4BS6tb/FD+I9yGEQdy+cQMxKxaUhCMYwwTvTRMX41b8FbZ4vQsLaZyihAZS1zbApf0kGeaCexjHYyzw/kviCnGQcWcmH5BFHjZaUD34GuCfZUn0GSckzxILqKCONpQbxbPEPdeYF98wEJBlUV2zrooWb1g8hWFntXtuAGU0PRW2ruIPDDnFvTRQQUMz1o4dxIzPAH7hjlPY3cAqjlGGMrBR0xXHDo4Nsz8jjkphAAHX/nxnKF4jgW0Y/4Rtw3ORQ5gVmiVPktfBjgYY/K65+S8qhuJfsG7YgnlyR4hRdo9NLGGg2wZavG8qfoJVQ/ECxhGFDdVbA2Y/sWQoXnYVr0L52cApn2VC91DKqkUxhSKUnw3kkTA8FzU8pMFJYgHKzwYqSJC8YCg+w8N23zVz3xqoI4GcZryBJwjjAsq3BhhrEF/gt26rcs0CMUKU4v410MQaUoaZL2IMZ1B+NtDGJixdc4ysSHFZHf8aaCOJfd35L19Q7uKP5pqWtwYsUJj4XnO+t/EGQ8ho/viUyLnpdQUO0Xkz2GJt4gaGkdYtM9dNEOe7bkC+kqWxjdeGmW9hBGntzCkuOVd7aaBP4jPD+Z7EEI4040VMyorO8drWNBDqaEBummWwrkmexbyh+CWmZBJzmjwtbCB43Qo8QhXKg3Pn+JXfCU+dg0lzXYaP6DkxhmmEA1J8gtkXPRa/oGhE8iyjAdUN7tsNyPF56aW4HLthV/Fmj9+S95wGPiKHImw0uujciVlEEMSK+T59A+5nIIRRRBiYIs5giVmu8f9t+SV0iDTeYlAe3DX3rvHcgFdyFD8g2SxxGevYwT6OkME5qmjB3cC6pgH/yEfUj5D8qA0To3B2y8g/W3EFMEBZY/QAAAAASUVORK5CYII=";

export default function DependencyInstallSection() {
  const { t } = useTranslation();
  const location = useLocation();
  const showSection = isLocalRuntime() || isDesktopRuntime();
  const [loading, setLoading] = useState(false);
  const [status, setStatus] = useState<FFmpegDependencyStatus | null>(null);
  const [loadError, setLoadError] = useState("");
  const [modalOpen, setModalOpen] = useState(false);
  const [customPath, setCustomPath] = useState("");
  const [saving, setSaving] = useState(false);
  const [installing, setInstalling] = useState(false);
  const [checking, setChecking] = useState(false);
  const [searchValue, setSearchValue] = useState("");

  const refresh = useCallback(async () => {
    setLoading(true);
    setLoadError("");
    try {
      const next = await getFFmpegDependencyStatus();
      setStatus(next);
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
    if (!showSection || location.hash !== "#ffmpeg-dependency") {
      return;
    }
    setModalOpen(true);
    requestAnimationFrame(() => {
      document
        .getElementById("ffmpeg-dependency")
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
  const shouldShowCard = useMemo(() => {
    if (!normalizedSearchValue) {
      return true;
    }
    const title = t("modelProvider.external.dependencyFfmpegTitle").toLowerCase();
    const summary = t("modelProvider.external.dependencyFfmpegSummary").toLowerCase();
    return title.includes(normalizedSearchValue) || summary.includes(normalizedSearchValue);
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
            {shouldShowCard ? (
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
          </div>
          {!loading && !loadError && !shouldShowCard ? (
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
    </>
  );
}
