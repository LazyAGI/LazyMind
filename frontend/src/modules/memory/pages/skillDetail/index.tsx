import { useEffect, useMemo, useState } from "react";
import { Alert, Button, Empty, Input, Space, Tag, message } from "antd";
import { HistoryOutlined } from "@ant-design/icons";
import { useParams } from "react-router-dom";
import { DetailPageHeader } from "@/components/ui";
import { getLocalizedErrorMessage } from "@/components/request";
import ResourceVersionDrawer from "../../components/ResourceVersionDrawer";
import SkillPackageEditor from "../../components/skillPackage/SkillPackageEditor";
import RouteLoading from "../../components/RouteLoading";
import { useMemoryManagementOutletContext } from "../../context";
import {
  buildSkillUpdatePayload,
  getSkillAssetDetail,
  patchSkillAsset,
} from "../../skillApi";
import { type StructuredAsset } from "../../shared";

export default function MemorySkillDetailPage() {
  const { itemId = "" } = useParams();
  const {
    t,
    skillAssets,
    skillsInitialized,
    navigateToMemoryList,
    refreshSkillAssets,
  } = useMemoryManagementOutletContext();
  const [detail, setDetail] = useState<StructuredAsset | null>(null);
  const [loading, setLoading] = useState(false);
  const [errorMessage, setErrorMessage] = useState("");
  const [retryKey, setRetryKey] = useState(0);
  const [isTitleEditing, setIsTitleEditing] = useState(false);
  const [titleDraft, setTitleDraft] = useState("");
  const [titleSaving, setTitleSaving] = useState(false);
  const [isDescriptionEditing, setIsDescriptionEditing] = useState(false);
  const [descriptionDraft, setDescriptionDraft] = useState("");
  const [descriptionSaving, setDescriptionSaving] = useState(false);
  const [versionDrawerOpen, setVersionDrawerOpen] = useState(false);

  const cachedSkill = useMemo(
    () => skillAssets.find((item: StructuredAsset) => item.id === itemId) || null,
    [itemId, skillAssets],
  );
  const skill = detail || cachedSkill;
  const canEditSkillDetail = Boolean(skill) && !skill?.readonly;

  const buildMetadataPatchPayload = (asset: StructuredAsset, overrides: Record<string, unknown> = {}) =>
    buildSkillUpdatePayload({
      name: asset.name,
      description: asset.description,
      category: asset.category,
      tags: asset.tags,
      autoEvo: asset.autoEvo,
      isEnabled: asset.isEnabled,
      ...overrides,
    });

  useEffect(() => {
    if (!skill || isTitleEditing) {
      return;
    }
    setTitleDraft(skill.name || "");
  }, [isTitleEditing, skill]);

  useEffect(() => {
    if (!skill || isDescriptionEditing) {
      return;
    }
    setDescriptionDraft(skill.description || "");
  }, [isDescriptionEditing, skill]);

  useEffect(() => {
    let ignore = false;

    if (!itemId) {
      setDetail(null);
      setErrorMessage("");
      return () => {
        ignore = true;
      };
    }

    setDetail(cachedSkill);

    if (!skillsInitialized && !cachedSkill) {
      return () => {
        ignore = true;
      };
    }

    setLoading(true);
    setErrorMessage("");
    void (async () => {
      try {
        const nextDetail = await getSkillAssetDetail(itemId, { loadContent: false });
        if (ignore) {
          return;
        }
        setDetail(nextDetail);
      } catch (error) {
        if (ignore) {
          return;
        }
        console.error("Load skill detail failed:", error);
        setErrorMessage(
          getLocalizedErrorMessage(error, t("admin.memorySkillDetailLoadFailed")) ||
            t("admin.memorySkillDetailLoadFailed"),
        );
      } finally {
        if (!ignore) {
          setLoading(false);
        }
      }
    })();

    return () => {
      ignore = true;
    };
  }, [cachedSkill, itemId, retryKey, skillsInitialized, t]);

  const handleSkillUpdated = async () => {
    if (!itemId) {
      return;
    }
    const nextDetail = await getSkillAssetDetail(itemId, { loadContent: false });
    if (nextDetail) {
      setDetail(nextDetail);
    }
    await refreshSkillAssets();
  };

  if ((loading || !skillsInitialized) && !skill && !errorMessage) {
    return <RouteLoading title={t("admin.memorySkillDetailTitle")} />;
  }

  const handleSaveTitleEdit = async () => {
    if (!skill || !canEditSkillDetail || titleSaving) {
      return;
    }
    const nextName = titleDraft.trim();
    if (!nextName) {
      message.warning(`${t("common.pleaseInput")}${t("admin.memoryName")}`);
      return;
    }
    if (nextName === skill.name) {
      setIsTitleEditing(false);
      return;
    }

    setTitleSaving(true);
    try {
      await patchSkillAsset(skill.id, buildMetadataPatchPayload(skill, { name: nextName }));
      await handleSkillUpdated();
      setIsTitleEditing(false);
      message.success(t("common.saveSuccess"));
    } catch (error) {
      console.error("Save skill title failed:", error);
      message.error(
        getLocalizedErrorMessage(error, t("common.saveFailed")) || t("common.saveFailed"),
      );
    } finally {
      setTitleSaving(false);
    }
  };

  const handleSaveDescriptionEdit = async () => {
    if (!skill || !canEditSkillDetail || descriptionSaving) {
      return;
    }
    const nextDescription = descriptionDraft.trim();
    if (nextDescription === (skill.description || "").trim()) {
      setIsDescriptionEditing(false);
      return;
    }

    setDescriptionSaving(true);
    try {
      await patchSkillAsset(
        skill.id,
        buildMetadataPatchPayload(skill, { description: nextDescription }),
      );
      await handleSkillUpdated();
      setIsDescriptionEditing(false);
      message.success(t("common.saveSuccess"));
    } catch (error) {
      console.error("Save skill description failed:", error);
      message.error(
        getLocalizedErrorMessage(error, t("common.saveFailed")) || t("common.saveFailed"),
      );
    } finally {
      setDescriptionSaving(false);
    }
  };

  const skillTitleNode = skill ? (
    isTitleEditing ? (
      <div className="memory-skill-detail-title-edit">
        <Input
          size="small"
          value={titleDraft}
          onChange={(event) => setTitleDraft(event.target.value)}
          onPressEnter={() => void handleSaveTitleEdit()}
        />
        <Space size={6}>
          <Button size="small" onClick={() => setIsTitleEditing(false)} disabled={titleSaving}>
            {t("common.cancel")}
          </Button>
          <Button
            size="small"
            type="primary"
            loading={titleSaving}
            onClick={() => void handleSaveTitleEdit()}
          >
            {t("common.save")}
          </Button>
        </Space>
      </div>
    ) : canEditSkillDetail ? (
      <button
        type="button"
        className="memory-skill-detail-title-trigger"
        onClick={() => setIsTitleEditing(true)}
      >
        {skill.name}
      </button>
    ) : (
      skill.name
    )
  ) : (
    t("admin.memorySkillDetailTitle")
  );

  const hasSkillMeta =
    Boolean(skill?.description?.trim()) ||
    Boolean(skill?.category) ||
    Boolean(skill?.draft?.hasUncommittedDraft) ||
    Boolean(skill?.tags.length);

  const skillMetaContent =
    skill && (hasSkillMeta || canEditSkillDetail) ? (
      <div className="memory-skill-detail-header">
      <div className="memory-skill-detail-description-row">
        {isDescriptionEditing ? (
          <div className="memory-skill-detail-description-edit">
            <Input.TextArea
              value={descriptionDraft}
              autoSize={{ minRows: 1, maxRows: 3 }}
              onChange={(event) => setDescriptionDraft(event.target.value)}
            />
            <Space size={6}>
              <Button size="small" onClick={() => setIsDescriptionEditing(false)} disabled={descriptionSaving}>
                {t("common.cancel")}
              </Button>
              <Button
                size="small"
                type="primary"
                loading={descriptionSaving}
                onClick={() => void handleSaveDescriptionEdit()}
              >
                {t("common.save")}
              </Button>
            </Space>
          </div>
        ) : canEditSkillDetail ? (
          <button
            type="button"
            className="memory-skill-detail-description-trigger"
            onClick={() => setIsDescriptionEditing(true)}
          >
            <span>{skill.description || "-"}</span>
          </button>
        ) : (
          <span className="memory-skill-detail-description-text">{skill.description || "-"}</span>
        )}
      </div>
      <div className="memory-skill-detail-meta">
        {skill.category ? (
          <Tag className="memory-category-tag" bordered={false}>
            {skill.category}
          </Tag>
        ) : null}
        {skill.draft?.hasUncommittedDraft ? (
          <Tag color="gold" bordered={false}>
            {t("admin.memorySkillDraftPending")}
          </Tag>
        ) : null}
        {skill.tags.map((item: string) => (
          <Tag key={item}>{item}</Tag>
        ))}
      </div>
      </div>
    ) : null;

  return (
    <div className="memory-skill-detail-layout">
      <DetailPageHeader
        className="memory-skill-detail-page-header"
        title={skillTitleNode}
        description={skillMetaContent}
        settingsMenu={
          skill ? (
            <Button icon={<HistoryOutlined />} onClick={() => setVersionDrawerOpen(true)}>
              {t("admin.memoryVersionHistoryButton")}
            </Button>
          ) : null
        }
        onBack={() => navigateToMemoryList("skills")}
      />

      {errorMessage ? (
        <Alert
          type="error"
          showIcon
          message={errorMessage}
          action={
            <Button size="small" onClick={() => setRetryKey((value) => value + 1)}>
              {t("common.retry")}
            </Button>
          }
        />
      ) : null}

      {!skill && !loading ? (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={t("admin.memoryDiffTargetMissing")}
        />
      ) : skill ? (
        <div className="memory-skill-detail-card memory-skill-package-card">
          <SkillPackageEditor
            skillId={skill.id}
            canEdit={canEditSkillDetail}
            t={t}
            onSkillUpdated={handleSkillUpdated}
          />
        </div>
      ) : null}

      <ResourceVersionDrawer
        open={versionDrawerOpen}
        resourceId={itemId}
        resourceName={skill?.name || itemId}
        resourceType="skill"
        t={t}
        onClose={() => setVersionDrawerOpen(false)}
      />
    </div>
  );
}
