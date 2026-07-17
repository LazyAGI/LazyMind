import { useMemo, useRef } from "react";
import {
  BellOutlined,
  DatabaseOutlined,
  EditOutlined,
  FileTextOutlined,
  IdcardOutlined,
  StarOutlined,
  SyncOutlined,
  UserOutlined,
} from "@ant-design/icons";
import { Button, Empty, Skeleton, Switch } from "antd";
import { useMemoryManagementOutletContext } from "../../context";
import type { ExperienceAsset } from "../../shared";

type ProfileFieldKey = "agentPersona" | "preferredName" | "responseStyle";

const isProfileAsset = (record: ExperienceAsset) => {
  const resourceType = String(record.resourceType || "").toLowerCase();
  return (
    resourceType.includes("user_preference") ||
    resourceType.includes("user-preference") ||
    resourceType.includes("preference") ||
    record.title === "用户画像"
  );
};

const isPendingAsset = (record: ExperienceAsset) => {
  const draftStatus = String(record.draftStatus || "").toLowerCase();
  const reviewStatus = String(record.reviewStatus || "").toLowerCase();
  return (
    Boolean(record.hasPendingReviewResult) ||
    Boolean(record.hasPendingReviewSuggestions) ||
    reviewStatus === "pending" ||
    draftStatus === "pending" ||
    draftStatus === "pending_confirm"
  );
};

export default function ExperienceOverview() {
  const pendingPanelRef = useRef<HTMLElement>(null);
  const assetPanelRef = useRef<HTMLElement>(null);
  const {
    t,
    filteredExperienceItems,
    experienceLoading,
    experienceAutoEvoLoading,
    experienceFeatureEnabled,
    experienceSettingSaving,
    handleExperienceFeatureToggle,
    handleExperienceAutoEvoToggle,
    openChangeReview,
    navigateToExperienceDetail,
  } = useMemoryManagementOutletContext();

  const profileAsset = useMemo(
    () => filteredExperienceItems.find(isProfileAsset) || null,
    [filteredExperienceItems],
  );
  const pendingAssets = useMemo(
    () => filteredExperienceItems.filter(isPendingAsset),
    [filteredExperienceItems],
  );
  const autoEvoEnabledCount = useMemo(
    () => filteredExperienceItems.filter((item: ExperienceAsset) => item.autoEvo).length,
    [filteredExperienceItems],
  );

  const profileFields: Array<{
    key: ProfileFieldKey;
    label: string;
    value: string;
    icon: React.ReactNode;
  }> = [
    {
      key: "agentPersona",
      label: t("admin.memoryProfileAgentPersona"),
      value: profileAsset?.agentPersona || "",
      icon: <IdcardOutlined />,
    },
    {
      key: "preferredName",
      label: t("admin.memoryProfilePreferredName"),
      value: profileAsset?.preferredName || "",
      icon: <UserOutlined />,
    },
    {
      key: "responseStyle",
      label: t("admin.memoryProfileResponseStyle"),
      value: profileAsset?.responseStyle || "",
      icon: <FileTextOutlined />,
    },
  ];

  const getAssetMeta = (record: ExperienceAsset) => {
    const isProfile = isProfileAsset(record);
    const profileSummary = [
      record.agentPersona,
      record.preferredName
        ? t("admin.memoryExperiencePreferredNameSummary", {
            name: record.preferredName,
          })
        : "",
      record.responseStyle,
    ]
      .filter(Boolean)
      .join("；");

    return {
      icon: isProfile ? <UserOutlined /> : <DatabaseOutlined />,
      title: isProfile
        ? t("admin.memoryExperienceProfileTitle")
        : t("admin.memoryExperienceWorkingMemoryTitle"),
      description: isProfile
        ? t("admin.memoryExperienceProfileDescription")
        : t("admin.memoryExperienceWorkingMemoryDescription"),
      summary:
        (isProfile ? profileSummary : record.summary) ||
        record.content ||
        t("admin.memoryProfileEmpty"),
    };
  };

  if (experienceLoading && !filteredExperienceItems.length) {
    return (
      <div className="memory-experience-overview is-loading" aria-busy="true">
        <Skeleton active paragraph={{ rows: 2 }} />
        <div className="memory-experience-skeleton-grid">
          <Skeleton.Node active />
          <Skeleton.Node active />
        </div>
      </div>
    );
  }

  if (!filteredExperienceItems.length) {
    return (
      <div className="memory-experience-overview-empty">
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={t("admin.memoryEmpty")}
        />
      </div>
    );
  }

  return (
    <div className="memory-experience-overview">
      <section
        className="memory-experience-status-grid"
        aria-label={t("admin.memoryExperienceStatusOverview")}
      >
        <div className="memory-experience-status-item is-positive">
          <span className="memory-experience-status-icon">
            <UserOutlined />
          </span>
          <span className="memory-experience-status-copy">
            <span>{t("admin.memoryExperienceAssetCount")}</span>
            <strong>
              {t("admin.memoryExperienceAssetCountValue", {
                count: filteredExperienceItems.length,
              })}
            </strong>
          </span>
        </div>

        <div className="memory-experience-status-item">
          <Switch
            aria-label={t("admin.memoryExperienceApplyInAnswers")}
            checked={experienceFeatureEnabled}
            loading={experienceSettingSaving}
            size="small"
            onChange={(checked: boolean) =>
              void handleExperienceFeatureToggle(checked)
            }
          />
          <span className="memory-experience-status-copy">
            <span>{t("admin.memoryExperienceApplyInAnswers")}</span>
            <strong>
              {experienceFeatureEnabled ? t("admin.enabled") : t("admin.disabled")}
            </strong>
          </span>
        </div>

        <button
          type="button"
          className="memory-experience-status-item is-button"
          onClick={() =>
            assetPanelRef.current?.scrollIntoView({
              behavior: "smooth",
              block: "nearest",
            })
          }
        >
          <span className="memory-experience-status-icon">
            <SyncOutlined />
          </span>
          <span className="memory-experience-status-copy">
            <span>{t("admin.memoryAutoUpdate")}</span>
            <strong>
              {t("admin.memoryExperienceAutoUpdateValue", {
                enabled: autoEvoEnabledCount,
                total: filteredExperienceItems.length,
              })}
            </strong>
          </span>
        </button>

        <button
          type="button"
          className="memory-experience-status-item is-button"
          disabled={!pendingAssets.length}
          onClick={() =>
            pendingPanelRef.current?.scrollIntoView({
              behavior: "smooth",
              block: "nearest",
            })
          }
        >
          <span className="memory-experience-status-icon">
            <BellOutlined />
          </span>
          <span className="memory-experience-status-copy">
            <span>{t("admin.memoryExperiencePendingResources")}</span>
            <strong>
              {t("admin.memoryExperiencePendingCountValue", {
                count: pendingAssets.length,
              })}
            </strong>
          </span>
        </button>
      </section>

      {pendingAssets.length ? (
        <section className="memory-experience-section" ref={pendingPanelRef}>
          <div className="memory-experience-section-heading">
            <span className="memory-experience-section-icon">
              <BellOutlined />
            </span>
            <div>
              <h3>
                {t("admin.memoryExperiencePendingTitle", {
                  count: pendingAssets.length,
                })}
              </h3>
              <p>{t("admin.memoryExperiencePendingDescription")}</p>
            </div>
          </div>
          <div className="memory-experience-pending-list">
            {pendingAssets.map((record: ExperienceAsset) => {
              const meta = getAssetMeta(record);
              return (
                <article className="memory-experience-pending-item" key={record.id}>
                  <span className="memory-experience-item-icon">{meta.icon}</span>
                  <strong>{meta.title}</strong>
                  <div className="memory-experience-pending-copy">
                    <span>{t("admin.memoryExperienceDraftExists")}</span>
                    <p>{t("admin.memoryExperienceDraftDescription", { name: meta.title })}</p>
                  </div>
                  <Button
                    type="primary"
                    onClick={() => void openChangeReview("experience", record.id)}
                  >
                    {t("admin.memoryPreferencePreviewButton")}
                  </Button>
                </article>
              );
            })}
          </div>
        </section>
      ) : null}

      {profileAsset ? (
        <section className="memory-experience-section">
          <div className="memory-experience-section-heading">
            <span className="memory-experience-section-icon">
              <StarOutlined />
            </span>
            <div>
              <h3>{t("admin.memoryExperienceCorePreferences")}</h3>
              <p>{t("admin.memoryExperienceCorePreferencesDescription")}</p>
            </div>
          </div>
          <div className="memory-experience-preference-grid">
            {profileFields.map((field) => (
              <article className="memory-experience-preference-card" key={field.key}>
                <span className="memory-experience-item-icon">{field.icon}</span>
                <div className="memory-experience-preference-copy">
                  <h4>{field.label}</h4>
                  <p>{field.value || t("admin.memoryProfileEmpty")}</p>
                  <span>{t("admin.memoryExperiencePreferenceActive")}</span>
                </div>
                <Button
                  aria-label={t("admin.memoryProfileEditTitle", { field: field.label })}
                  icon={<EditOutlined />}
                  size="small"
                  type="text"
                  onClick={() => navigateToExperienceDetail(profileAsset.id)}
                />
              </article>
            ))}
          </div>
        </section>
      ) : null}

      <section
        className="memory-experience-asset-grid"
        ref={assetPanelRef}
        aria-label={t("admin.memoryExperienceAssets")}
      >
        {filteredExperienceItems.map((record: ExperienceAsset) => {
          const meta = getAssetMeta(record);
          return (
            <article className="memory-experience-asset-card" key={record.id}>
              <div className="memory-experience-asset-heading">
                <div className="memory-experience-section-heading">
                  <span className="memory-experience-section-icon">{meta.icon}</span>
                  <div>
                    <h3>{meta.title}</h3>
                    <p>{meta.description}</p>
                  </div>
                </div>
                <label className="memory-experience-auto-evo">
                  <span>{t("admin.memoryAutoUpdate")}</span>
                  <Switch
                    checked={Boolean(record.autoEvo)}
                    loading={experienceAutoEvoLoading.has(record.id)}
                    size="small"
                    onChange={(checked: boolean) =>
                      void handleExperienceAutoEvoToggle(record, checked)
                    }
                  />
                </label>
              </div>
              <div className="memory-experience-summary-row">
                <span className="memory-experience-item-icon">
                  <FileTextOutlined />
                </span>
                <div>
                  <strong>{t("admin.memoryContentSummary")}</strong>
                  <p>{meta.summary}</p>
                </div>
                <Button
                  aria-label={t("admin.memoryExperienceEditAsset", { name: meta.title })}
                  icon={<EditOutlined />}
                  size="small"
                  type="text"
                  onClick={() => navigateToExperienceDetail(record.id)}
                />
              </div>
            </article>
          );
        })}
      </section>
    </div>
  );
}
