import { useEffect, useMemo, useState } from "react";
import { Alert, Button, Empty, Input, Space, Tag, message } from "antd";
import { HistoryOutlined } from "@ant-design/icons";
import { useParams } from "react-router-dom";
import MarkdownViewer from "@/modules/knowledge/components/MarkdownViewer";
import { DetailPageHeader } from "@/components/ui";
import { getLocalizedErrorMessage } from "@/components/request";
import ResourceVersionDrawer from "../../components/ResourceVersionDrawer";
import RouteLoading from "../../components/RouteLoading";
import { useMemoryManagementOutletContext } from "../../context";
import { buildSkillUpdatePayload, getSkillAssetDetail, patchSkillAsset } from "../../skillApi";
import { getSkillBodyContentForDisplay, type StructuredAsset } from "../../shared";

const markdownExtensions = new Set(["md", "markdown"]);

const hasMarkdownShape = (content: string) =>
  /^#{1,6}\s+\S/m.test(content) ||
  /```[\s\S]*?```/.test(content) ||
  /^\s*[-*+]\s+\S/m.test(content) ||
  /^\s*\d+\.\s+\S/m.test(content) ||
  /\[[^\]]+\]\([^)]+\)/.test(content) ||
  /^\s*>\s+\S/m.test(content);

const isMarkdownSkill = (asset: StructuredAsset) => {
  const ext = (asset.fileExt || "").trim().toLowerCase().replace(/^\./, "");
  return markdownExtensions.has(ext) || hasMarkdownShape(asset.content || "");
};

const stripLeadingMetaLines = (content: string) => {
  return getSkillBodyContentForDisplay(content);
};

const contentForPatch = (asset: StructuredAsset) =>
  asset.parentId ? asset.content || "" : stripLeadingMetaLines(asset.content || "");

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
  const [isInlineEditing, setIsInlineEditing] = useState(false);
  const [inlineContentDraft, setInlineContentDraft] = useState("");
  const [inlineSaving, setInlineSaving] = useState(false);
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
  const canEditSkillDetail = Boolean(skill) && !skill?.readonly && !skill?.isBuiltinTemplate;
  const renderAsMarkdown = skill ? isMarkdownSkill(skill) : false;
  const previewContent = useMemo(
    () => stripLeadingMetaLines(skill?.content || ""),
    [skill?.content],
  );
  const resolveParentSkillName = (asset: StructuredAsset) =>
    asset.parentSkillName ||
    (asset.parentId
      ? skillAssets.find((item: StructuredAsset) => item.id === asset.parentId)?.name || ""
      : "");
  const buildPatchPayload = (asset: StructuredAsset, overrides: Record<string, unknown> = {}) =>
    buildSkillUpdatePayload(
      {
        ...asset,
        content: contentForPatch(asset),
        parentSkillName: resolveParentSkillName(asset),
      },
      {
        is_locked: Boolean(asset.protect),
        ...overrides,
      },
    );

  useEffect(() => {
    if (!skill || isInlineEditing) {
      return;
    }
    setInlineContentDraft(stripLeadingMetaLines(skill.content || ""));
  }, [isInlineEditing, skill]);

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
        const nextDetail = await getSkillAssetDetail(itemId);
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

  if ((loading || !skillsInitialized) && !skill && !errorMessage) {
    return <RouteLoading title={t("admin.memorySkillDetailTitle")} />;
  }

  const handleStartInlineEdit = () => {
    if (!canEditSkillDetail) {
      return;
    }
    setInlineContentDraft(previewContent);
    setIsInlineEditing(true);
  };

  const handleCancelInlineEdit = () => {
    setInlineContentDraft(previewContent);
    setIsInlineEditing(false);
  };

  const handleSaveInlineEdit = async () => {
    if (!skill) {
      return;
    }
    if (!canEditSkillDetail) {
      return;
    }

    if (inlineSaving) {
      return;
    }

    const trimmedDraft = inlineContentDraft.trim();
    if (trimmedDraft === previewContent.trim()) {
      setIsInlineEditing(false);
      return;
    }

    const patchPayload = buildPatchPayload(skill, {
      content: inlineContentDraft,
    });

    setInlineSaving(true);
    try {
      await patchSkillAsset(skill.id, patchPayload);
      const latestDetail = await getSkillAssetDetail(skill.id);
      if (latestDetail) {
        setDetail(latestDetail);
      } else {
        setDetail((previous) =>
          previous
            ? {
                ...previous,
                content: inlineContentDraft,
              }
            : previous,
        );
      }
      await refreshSkillAssets();
      setIsInlineEditing(false);
      message.success(t("common.saveSuccess"));
    } catch (error) {
      console.error("Save skill detail inline edit failed:", error);
      message.error(
        getLocalizedErrorMessage(error, t("common.saveFailed")) || t("common.saveFailed"),
      );
    } finally {
      setInlineSaving(false);
    }
  };

  const handleStartTitleEdit = () => {
    if (!skill || !canEditSkillDetail) {
      return;
    }
    setTitleDraft(skill.name || "");
    setIsTitleEditing(true);
  };

  const handleCancelTitleEdit = () => {
    setTitleDraft(skill?.name || "");
    setIsTitleEditing(false);
  };

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

    const patchPayload = buildPatchPayload(skill, {
      name: nextName,
    });

    setTitleSaving(true);
    try {
      await patchSkillAsset(skill.id, patchPayload);
      const latestDetail = await getSkillAssetDetail(skill.id);
      if (latestDetail) {
        setDetail(latestDetail);
      }
      await refreshSkillAssets();
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

  const handleStartDescriptionEdit = () => {
    if (!skill || !canEditSkillDetail) {
      return;
    }
    setDescriptionDraft(skill.description || "");
    setIsDescriptionEditing(true);
  };

  const handleCancelDescriptionEdit = () => {
    setDescriptionDraft(skill?.description || "");
    setIsDescriptionEditing(false);
  };

  const handleSaveDescriptionEdit = async () => {
    if (!skill || !canEditSkillDetail || descriptionSaving) {
      return;
    }
    const nextDescription = descriptionDraft.trim();
    if (!nextDescription) {
      message.warning(`${t("common.pleaseInput")}${t("admin.memoryDescription")}`);
      return;
    }
    if (nextDescription === skill.description) {
      setIsDescriptionEditing(false);
      return;
    }

    const patchPayload = buildPatchPayload(skill, {
      description: nextDescription,
    });

    setDescriptionSaving(true);
    try {
      await patchSkillAsset(skill.id, patchPayload);
      const latestDetail = await getSkillAssetDetail(skill.id);
      if (latestDetail) {
        setDetail(latestDetail);
      }
      await refreshSkillAssets();
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

  const skillHeaderContent = skill ? (
    <div className="memory-skill-detail-header-content">
      <div className="memory-skill-detail-title">
        <div className={`memory-skill-detail-title-copy${isTitleEditing ? " is-editing" : ""}`}>
          {isTitleEditing && canEditSkillDetail ? (
            <div
              className="memory-skill-detail-title-editor"
              onBlur={(event) => {
                const nextFocusedNode = event.relatedTarget as Node | null;
                if (event.currentTarget.contains(nextFocusedNode)) {
                  return;
                }
                void handleSaveTitleEdit();
              }}
            >
              <Input
                autoFocus
                value={titleDraft}
                onChange={(event) => setTitleDraft(event.target.value)}
                onPressEnter={() => void handleSaveTitleEdit()}
                onKeyDown={(event) => {
                  if (event.key === "Escape") {
                    event.preventDefault();
                    handleCancelTitleEdit();
                  }
                }}
                disabled={titleSaving}
                className="memory-skill-detail-title-input"
              />
            </div>
          ) : (
            <>
              {canEditSkillDetail ? (
                <button
                  type="button"
                  className="memory-skill-detail-title-trigger"
                  onClick={handleStartTitleEdit}
                >
                  <h3>{skill.name}</h3>
                </button>
              ) : (
                <h3>{skill.name}</h3>
              )}
              {isDescriptionEditing && canEditSkillDetail ? (
                <div
                  className="memory-skill-detail-description-editor"
                  onBlur={(event) => {
                    const nextFocusedNode = event.relatedTarget as Node | null;
                    if (event.currentTarget.contains(nextFocusedNode)) {
                      return;
                    }
                    void handleSaveDescriptionEdit();
                  }}
                >
                  <Input.TextArea
                    autoFocus
                    value={descriptionDraft}
                    onChange={(event) => setDescriptionDraft(event.target.value)}
                    autoSize={{ minRows: 2, maxRows: 5 }}
                    disabled={descriptionSaving}
                    onKeyDown={(event) => {
                      if (event.key === "Escape") {
                        event.preventDefault();
                        handleCancelDescriptionEdit();
                      }
                    }}
                    className="memory-skill-detail-description-input"
                  />
                </div>
              ) : canEditSkillDetail ? (
                <button
                  type="button"
                  className="memory-skill-detail-description-trigger"
                  onClick={handleStartDescriptionEdit}
                >
                  <p>{skill.description || "-"}</p>
                </button>
              ) : (
                <p>{skill.description || "-"}</p>
              )}
            </>
          )}
        </div>
        <div className="memory-skill-detail-meta">
          {skill.category ? (
            <Tag className="memory-category-tag" bordered={false}>
              {skill.category}
            </Tag>
          ) : null}
          {skill.protect ? (
            <Tag className="memory-protect-tag" bordered={false}>
              {t("admin.memoryProtect", { defaultValue: "保护" })}
            </Tag>
          ) : null}
        </div>
      </div>

      {skill.tags.length ? (
        <div className="memory-skill-detail-tags">
          <div className="memory-tag-group">
            {skill.tags.map((item: string) => (
              <Tag key={item}>{item}</Tag>
            ))}
          </div>
        </div>
      ) : null}
    </div>
  ) : (
    t("admin.memorySkillShareUnknownSkill")
  );

  return (
    <div className="memory-skill-detail-layout">
      <DetailPageHeader
        className="memory-skill-detail-page-header"
        title={t("admin.memorySkillDetailTitle")}
        description={skillHeaderContent}
        settingsMenu={
          skill ? (
            <Button
              icon={<HistoryOutlined />}
              onClick={() => setVersionDrawerOpen(true)}
            >
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
        <div className="memory-skill-detail-card">
          <div className="memory-form-field memory-form-field-full">
            <div className="memory-skill-detail-editor-toolbar">
              <label>
                {renderAsMarkdown
                  ? t("admin.memorySkillDetailMarkdownPreview")
                  : t("admin.memorySkillDetailPlainPreview")}
              </label>
              <Space size={8}>
                {isInlineEditing ? (
                  <>
                    <Button onClick={handleCancelInlineEdit} disabled={inlineSaving}>
                      {t("common.cancel")}
                    </Button>
                    <Button
                      type="primary"
                      loading={inlineSaving}
                      onClick={() => void handleSaveInlineEdit()}
                    >
                      {t("common.save")}
                    </Button>
                  </>
                ) : canEditSkillDetail ? (
                  <Button onClick={handleStartInlineEdit}>
                    {t("common.edit")}
                  </Button>
                ) : null}
              </Space>
            </div>
            <div className="memory-skill-detail-content">
              {isInlineEditing ? (
                <Input.TextArea
                  value={inlineContentDraft}
                  onChange={(event) => setInlineContentDraft(event.target.value)}
                  autoSize={{ minRows: 18 }}
                  className="memory-skill-detail-textarea"
                />
              ) : renderAsMarkdown ? (
                <MarkdownViewer>{previewContent}</MarkdownViewer>
              ) : (
                <pre>{previewContent || "-"}</pre>
              )}
            </div>
          </div>
        </div>
      ) : null}

      {skill ? (
        <ResourceVersionDrawer
          open={versionDrawerOpen}
          resourceId={skill.id}
          resourceName={skill.name}
          resourceType="skill"
          t={t}
          onClose={() => setVersionDrawerOpen(false)}
        />
      ) : null}
    </div>
  );
}
