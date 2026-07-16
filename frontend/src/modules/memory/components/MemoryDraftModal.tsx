import { useState } from "react";
import {
  Alert,
  Button,
  Input,
  Modal,
  Select,
  Tooltip,
  Upload,
  message,
} from "antd";
import { UploadOutlined } from "@ant-design/icons";
import {
  GLOSSARY_ALIAS_MAX_LENGTH,
  GLOSSARY_CONTENT_MAX_LENGTH,
  GLOSSARY_TERM_MAX_LENGTH,
  SKILL_TAG_MAX_COUNT,
} from "../shared";

export type SkillCreateSource = "zip" | "url";

interface MemoryDraftModalProps {
  t: any;
  modalOpen: boolean;
  modalTitle: string;
  closeModal: () => void;
  saveDraft: () => Promise<void>;
  activeTab: string;
  experienceSaving: boolean;
  glossarySaving: boolean;
  skillSaving: boolean;
  isReadOnly: boolean;
  draft: any;
  setDraft: any;
  pendingGlossaryMergeSourceIds: string[];
  modalMode: string;
  tagOptions: Array<{ label: string; value: string }>;
  normalizeTagValues: (values: string[]) => string[];
  handleImportSkillPackage: (file: File) => void;
  pendingSkillPackageFile?: File | null;
  pendingSkillSourceUrl?: string;
}

export default function MemoryDraftModal(props: MemoryDraftModalProps) {
  const {
    t,
    modalOpen,
    modalTitle,
    closeModal,
    saveDraft,
    activeTab,
    experienceSaving,
    glossarySaving,
    skillSaving,
    isReadOnly,
    draft,
    setDraft,
    pendingGlossaryMergeSourceIds,
    modalMode,
    tagOptions,
    normalizeTagValues,
    handleImportSkillPackage,
    pendingSkillPackageFile = null,
    pendingSkillSourceUrl = "",
  } = props;
  const [glossaryAliasInput, setGlossaryAliasInput] = useState("");
  const isSkillCreateModal = activeTab === "skills" && modalMode === "add";
  const isSkillEditModal = activeTab === "skills" && modalMode === "edit";
  const isExternalSkillImport = Boolean(
    pendingSkillPackageFile || pendingSkillSourceUrl.trim(),
  );

  const handleGlossaryAliasesChange = (value: string[]) => {
    const normalizedAliases = Array.from(
      new Set((value || []).map((item) => item.trim()).filter(Boolean)),
    );
    const validAliases = normalizedAliases.filter(
      (item) => item.length <= GLOSSARY_ALIAS_MAX_LENGTH,
    );

    if (validAliases.length < normalizedAliases.length) {
      message.warning(
        t("admin.memoryGlossaryAliasMaxLength", {
          count: GLOSSARY_ALIAS_MAX_LENGTH,
        }),
      );
    }

    setGlossaryAliasInput("");
    setDraft((previous: any) => ({ ...previous, aliases: validAliases }));
  };

  return (
    <Modal
      open={modalOpen}
      title={modalTitle}
      onCancel={closeModal}
      onOk={isReadOnly ? closeModal : saveDraft}
      confirmLoading={
        activeTab === "experience"
          ? experienceSaving
          : activeTab === "glossary"
            ? glossarySaving
            : activeTab === "skills"
              ? skillSaving
              : false
      }
      okText={isReadOnly ? t("common.close") : t("common.save")}
      cancelText={t("common.cancel")}
      destroyOnClose
      width={760}
      className={
        [
          isReadOnly ? "memory-readonly-modal" : undefined,
          isSkillCreateModal ? "memory-skill-create-modal" : undefined,
        ]
          .filter(Boolean)
          .join(" ") || undefined
      }
    >
      {activeTab === "experience" ? (
        <div className="memory-modal-grid">
          <div className="memory-form-field">
            <label>{t("admin.memoryTitle")}</label>
            <Input
              value={draft.title}
              readOnly={isReadOnly || modalMode === "edit"}
              className={
                modalMode === "edit"
                  ? "memory-experience-title-readonly"
                  : undefined
              }
              placeholder={t("common.pleaseInput") + t("admin.memoryTitle")}
              onChange={(event) =>
                setDraft((previous: any) => ({
                  ...previous,
                  title: event.target.value,
                }))
              }
            />
          </div>
          <div className="memory-form-field memory-form-field-full">
            <label>{t("admin.memoryContent")}</label>
            <Input.TextArea
              rows={9}
              value={draft.content}
              readOnly={isReadOnly}
              placeholder={t("common.pleaseInput") + t("admin.memoryContent")}
              onChange={(event) =>
                setDraft((previous: any) => ({
                  ...previous,
                  content: event.target.value,
                }))
              }
            />
          </div>
        </div>
      ) : activeTab === "glossary" ? (
        <div className="memory-modal-grid">
          {pendingGlossaryMergeSourceIds.length ? (
            <Alert
              type="info"
              showIcon
              className="memory-form-field memory-form-field-full"
              message={t("admin.memoryGlossaryBatchMergeDraftHint", {
                count: pendingGlossaryMergeSourceIds.length,
              })}
            />
          ) : null}
          <div className="memory-form-field memory-form-field-full">
            <label>{t("admin.memoryGlossaryTerm")}</label>
            <Input
              value={draft.term}
              maxLength={GLOSSARY_TERM_MAX_LENGTH}
              showCount
              readOnly={isReadOnly}
              placeholder={
                t("common.pleaseInput") + t("admin.memoryGlossaryTerm")
              }
              onChange={(event) =>
                setDraft((previous: any) => ({
                  ...previous,
                  term: event.target.value,
                }))
              }
            />
          </div>
          <div className="memory-form-field memory-form-field-full">
            <label>{t("admin.memoryGlossaryAliases")}</label>
            <Select
              mode="tags"
              searchValue={glossaryAliasInput}
              value={draft.aliases}
              disabled={isReadOnly}
              open={false}
              suffixIcon={null}
              placeholder={t("admin.memoryGlossaryAliasesPlaceholder")}
              onChange={handleGlossaryAliasesChange}
              onSearch={(value) =>
                setGlossaryAliasInput(value.slice(0, GLOSSARY_ALIAS_MAX_LENGTH))
              }
              onSelect={() => setGlossaryAliasInput("")}
              onInputKeyDown={(event) => {
                const navigationKeys = [
                  "Backspace",
                  "Delete",
                  "ArrowLeft",
                  "ArrowRight",
                  "ArrowUp",
                  "ArrowDown",
                  "Home",
                  "End",
                  "Tab",
                  "Enter",
                ];
                if (
                  !event.nativeEvent.isComposing &&
                  !event.ctrlKey &&
                  !event.metaKey &&
                  glossaryAliasInput.length >= GLOSSARY_ALIAS_MAX_LENGTH &&
                  !navigationKeys.includes(event.key)
                ) {
                  event.preventDefault();
                }
              }}
            />
          </div>
          <div className="memory-form-field memory-form-field-full memory-glossary-content-field">
            <label>{t("admin.memoryContent")}</label>
            <Input.TextArea
              rows={10}
              maxLength={GLOSSARY_CONTENT_MAX_LENGTH}
              showCount
              value={draft.content}
              readOnly={isReadOnly}
              placeholder={t("common.pleaseInput") + t("admin.memoryContent")}
              onChange={(event) =>
                setDraft((previous: any) => ({
                  ...previous,
                  content: event.target.value,
                }))
              }
            />
          </div>
        </div>
      ) : (
        <div
          className={[
            "memory-modal-grid",
            isSkillCreateModal ? "memory-skill-create-grid" : undefined,
          ]
            .filter(Boolean)
            .join(" ")}
        >
          {isSkillEditModal ? (
            <Alert
              type="info"
              showIcon
              className="memory-form-field memory-form-field-full"
              message={t("admin.memorySkillEditMetadataHint")}
            />
          ) : null}
          <div className="memory-form-field memory-form-field-full">
            <label>{t("admin.memoryName")}</label>
            <Input
              value={draft.name}
              readOnly={isReadOnly}
              placeholder={t("common.pleaseInput") + t("admin.memoryName")}
              onChange={(event) =>
                setDraft((previous: any) => ({
                  ...previous,
                  name: event.target.value,
                }))
              }
            />
          </div>
          <div className="memory-form-field memory-form-field-full">
            <label>{t("admin.memoryDescription")}</label>
            <Input.TextArea
              rows={isSkillCreateModal ? 2 : 3}
              autoSize={{
                minRows: isSkillCreateModal ? 2 : 3,
                maxRows: isSkillCreateModal ? 4 : 6,
              }}
              value={draft.description}
              readOnly={isReadOnly}
              placeholder={
                t("common.pleaseInput") + t("admin.memoryDescription")
              }
              onChange={(event) =>
                setDraft((previous: any) => ({
                  ...previous,
                  description: event.target.value,
                }))
              }
            />
          </div>
          <div className="memory-form-field">
            <label>{t("admin.memoryCategory")}</label>
            <Input
              value={draft.category}
              readOnly={isReadOnly}
              placeholder={t("admin.memoryCategoryPlaceholder")}
              onChange={(event) =>
                setDraft((previous: any) => ({
                  ...previous,
                  category: event.target.value,
                }))
              }
            />
          </div>
          <div className="memory-form-field memory-form-field-full">
            <label>{t("admin.memoryTagSet")}</label>
            <Select
              mode="tags"
              allowClear
              showSearch
              optionFilterProp="label"
              tokenSeparators={[",", "，"]}
              style={{ width: "100%" }}
              value={draft.tags}
              disabled={isReadOnly}
              placeholder={t("admin.memoryTagsPlaceholder")}
              onChange={(value) => {
                const normalizedTags = normalizeTagValues(value);
                if (normalizedTags.length > SKILL_TAG_MAX_COUNT) {
                  message.warning(
                    t("admin.memorySkillTagMaxCount", {
                      count: SKILL_TAG_MAX_COUNT,
                    }),
                  );
                }
                setDraft((previous: any) => ({
                  ...previous,
                  tags: normalizedTags.slice(0, SKILL_TAG_MAX_COUNT),
                }));
              }}
              options={tagOptions}
            />
            {!isSkillCreateModal ? (
              <span className="memory-form-hint">
                {t("admin.memoryTagsHint")}
              </span>
            ) : null}
          </div>
          {isSkillCreateModal ? (
            isExternalSkillImport ? (
              <div className="memory-form-field memory-form-field-full">
                {pendingSkillPackageFile ? (
                  <Alert
                    type="info"
                    showIcon
                    message={t("admin.memorySkillUploadFileTitle")}
                    description={t("admin.memorySkillUploadFileReady", {
                      size: Math.max(
                        1,
                        Math.round(pendingSkillPackageFile.size / 1024),
                      ),
                    })}
                  />
                ) : (
                  <Alert
                    type="info"
                    showIcon
                    message={t("admin.memorySkillCreateImportTitle")}
                    description={pendingSkillSourceUrl}
                  />
                )}
              </div>
            ) : (
              <div className="memory-form-field memory-form-field-full memory-skill-content-field">
                <div className="memory-skill-content-header">
                  <label>{t("admin.memoryMarkdown")}</label>
                  <Upload
                    accept=".md,.markdown"
                    multiple={false}
                    disabled={isReadOnly}
                    showUploadList={false}
                    beforeUpload={(file) => {
                      const ext = file.name.toLowerCase().replace(/^.*(\.[^.]+)$/, "$1");
                      if (ext !== ".md" && ext !== ".markdown") {
                        message.warning(t("admin.memorySkillImportMdFileTypeError"));
                        return false;
                      }
                      if (file.size > 512 * 1024) {
                        message.warning(t("admin.memorySkillImportMdFileSizeError"));
                        return false;
                      }
                      handleImportSkillPackage(file);
                      return false;
                    }}
                  >
                    <Tooltip
                      title={t("admin.memorySkillImportMdFileTooltip")}
                      placement="topRight"
                    >
                      <Button
                        type="text"
                        size="small"
                        icon={<UploadOutlined />}
                        disabled={isReadOnly}
                      >
                        {t("admin.memorySkillImportMdFile")}
                      </Button>
                    </Tooltip>
                  </Upload>
                </div>
                <Input.TextArea
                  rows={6}
                  autoSize={{ minRows: 6, maxRows: 12 }}
                  value={draft.content}
                  readOnly={isReadOnly}
                  placeholder={t("admin.memorySkillCreateManualPlaceholder")}
                  onChange={(event) =>
                    setDraft((previous: any) => ({
                      ...previous,
                      content: event.target.value,
                    }))
                  }
                />
                <span className="memory-form-hint">
                  {t("admin.memorySkillCreateManualHint")}
                </span>
              </div>
            )
          ) : null}
        </div>
      )}
    </Modal>
  );
}
