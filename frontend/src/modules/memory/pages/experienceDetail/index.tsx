import { Button, Empty, Tag } from "antd";
import { HistoryOutlined, LockOutlined } from "@ant-design/icons";
import { useMemo, useState } from "react";
import { useParams } from "react-router-dom";
import { DetailPageHeader } from "@/components/ui";
import PersonalResourceContentEditor from "../../components/personalResource/PersonalResourceContentEditor";
import ResourceVersionDrawer from "../../components/ResourceVersionDrawer";
import RouteLoading from "../../components/RouteLoading";
import { useMemoryManagementOutletContext } from "../../context";
import type { ExperienceAsset } from "../../shared";
import type { ResourceVersionType } from "../../resourceVersionApi";

const resolveExperienceResourceVersionType = (
  resourceType?: string,
): ResourceVersionType => {
  const normalized = (resourceType || "").trim().toLowerCase();
  return normalized.includes("memory") && !normalized.includes("preference")
    ? "memory"
    : "user_preference";
};

export default function MemoryExperienceDetailPage() {
  const { itemId = "" } = useParams();
  const {
    t,
    experienceAssets,
    experienceInitialized,
    navigateToMemoryList,
    refreshExperienceSection,
  } = useMemoryManagementOutletContext();
  const [versionDrawerOpen, setVersionDrawerOpen] = useState(false);

  const experience = useMemo(
    () => experienceAssets.find((item: ExperienceAsset) => item.id === itemId) || null,
    [experienceAssets, itemId],
  );
  const resourceVersionType = resolveExperienceResourceVersionType(
    experience?.resourceType,
  );
  const canEditExperience = Boolean(experience) && !experience?.protect;

  if (!experienceInitialized && !experience) {
    return <RouteLoading title={t("admin.memoryExperienceDetailTitle")} />;
  }

  return (
    <div className="memory-experience-detail-layout">
      <DetailPageHeader
        className="memory-experience-detail-page-header"
        title={t("admin.memoryExperienceDetailTitle")}
        description={experience?.title || t("admin.memoryDiffTargetMissing")}
        settingsMenu={
          experience ? (
            <Button
              icon={<HistoryOutlined />}
              onClick={() => setVersionDrawerOpen(true)}
            >
              {t("admin.memoryVersionHistoryButton")}
            </Button>
          ) : null
        }
        onBack={() => navigateToMemoryList("experience")}
      />

      {!experience ? (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={t("admin.memoryDiffTargetMissing")}
        />
      ) : (
        <div className="memory-experience-detail-card">
          <div className="memory-experience-detail-title">
            <div className="memory-experience-detail-title-copy">
              <h3>{experience.title}</h3>
            </div>
            <div className="memory-skill-detail-meta">
              {experience.protect ? (
                <Tag className="memory-protect-tag" bordered={false}>
                  <LockOutlined />
                  <span>{t("admin.memoryProtect", { defaultValue: "保护" })}</span>
                </Tag>
              ) : null}
            </div>
          </div>

          <div className="memory-experience-detail-body">
            <PersonalResourceContentEditor
              resourceType={experience.resourceType}
              canEdit={canEditExperience}
              t={t}
              onUpdated={refreshExperienceSection}
            />
          </div>
        </div>
      )}

      {experience ? (
        <ResourceVersionDrawer
          open={versionDrawerOpen}
          resourceId={experience.id}
          resourceName={experience.title}
          resourceType={resourceVersionType}
          t={t}
          onClose={() => setVersionDrawerOpen(false)}
        />
      ) : null}
    </div>
  );
}
