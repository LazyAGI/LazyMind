import { useState } from "react";
import { Alert, Button, Input, Modal, Select, Upload, message } from "antd";
import { DeleteOutlined, UploadOutlined } from "@ant-design/icons";
import {
  GLOSSARY_ALIAS_MAX_LENGTH,
  GLOSSARY_CONTENT_MAX_LENGTH,
  GLOSSARY_TERM_MAX_LENGTH,
  SKILL_TAG_MAX_COUNT,
} from "../shared";

interface MemoryDraftModalProps {
  t: any;
  modalOpen: boolean;
  modalTitle: string;
  closeModal: () => void;
  saveDraft: () => Promise<void>;
  activeTab: string;
  experienceSaving: boolean;
  glossarySaving: boolean;
  isReadOnly: boolean;
  draft: any;
  setDraft: any;
  pendingGlossaryMergeSourceIds: string[];
  modalMode: string;
  isChildSkillDraft: boolean;
  parentSkillOptions: Array<{ label: string; value: string }>;
  parentSkillOptionsLoading: boolean;
  tagOptions: Array<{ label: string; value: string }>;
  normalizeTagValues: (values: string[]) => string[];
  createSkillUploadProps: (childTempId?: string) => any;
  addChildSkillDraft: () => void;
  removeChildSkillDraft: (tempId: string) => void;
  updateChildSkillDraft: (
    tempId: string,
    patch: { name?: string; description?: string; tags?: string[]; content?: string },
  ) => void;
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
    isReadOnly,
    draft,
    setDraft,
    pendingGlossaryMergeSourceIds,
    modalMode,
    isChildSkillDraft,
    parentSkillOptions,
    parentSkillOptionsLoading,
    tagOptions,
    normalizeTagValues,
    createSkillUploadProps,
    addChildSkillDraft,
    removeChildSkillDraft,
    updateChildSkillDraft,
  } = props;
  const [glossaryAliasInput, setGlossaryAliasInput] = useState("");
  const shouldShowSkillContentEditor = !(activeTab === "skills" && modalMode === "edit");

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
            : false
      }
      okText={isReadOnly ? t("common.close") : t("common.save")}
      cancelText={t("common.cancel")}
      destroyOnClose
      width={760}
      className={isReadOnly ? "memory-readonly-modal" : undefined}
    >
      {activeTab === "experience" ? (
        <div className="memory-modal-grid">
          <div className="memory-form-field">
            <label>{t("admin.memoryTitle")}</label>
            <Input
              value={draft.title}
              readOnly={isReadOnly || modalMode === "edit"}
              className={modalMode === "edit" ? "memory-experience-title-readonly" : undefined}
              placeholder={t("common.pleaseInput") + t("admin.memoryTitle")}
              onChange={(event) =>
                setDraft((previous: any) => ({ ...previous, title: event.target.value }))
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
                setDraft((previous: any) => ({ ...previous, content: event.target.value }))
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
              placeholder={t("common.pleaseInput") + t("admin.memoryGlossaryTerm")}
              onChange={(event) =>
                setDraft((previous: any) => ({ ...previous, term: event.target.value }))
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
                setDraft((previous: any) => ({ ...previous, content: event.target.value }))
              }
            />
          </div>
        </div>
      ) : (
        <div className="memory-modal-grid">
          <div className="memory-form-field memory-form-field-full">
            <label>{t("admin.memoryName")}</label>
            <Input
              value={draft.name}
              readOnly={isReadOnly}
              placeholder={t("common.pleaseInput") + t("admin.memoryName")}
              onChange={(event) =>
                setDraft((previous: any) => ({ ...previous, name: event.target.value }))
              }
            />
          </div>
          <div className="memory-form-field memory-form-field-full">
            <label>{t("admin.memoryDescription")}</label>
            <Input.TextArea
              rows={3}
              autoSize={{ minRows: 3, maxRows: 6 }}
              value={draft.description}
              readOnly={isReadOnly}
              placeholder={t("common.pleaseInput") + t("admin.memoryDescription")}
              onChange={(event) =>
                setDraft((previous: any) => ({
                  ...previous,
                  description: event.target.value,
                }))
              }
            />
          </div>
          {activeTab === "skills" ? (
            <div className="memory-form-field">
              <label>{t("admin.memoryParentSkill")}</label>
              <Select
                allowClear
                showSearch
                optionFilterProp="label"
                value={draft.parentId || undefined}
                disabled={isReadOnly}
                loading={parentSkillOptionsLoading}
                placeholder={t("admin.memoryParentSkillPlaceholder")}
                options={parentSkillOptions}
                onChange={(value) =>
                  setDraft((previous: any) => ({
                    ...previous,
                    parentId: value || "",
                    childSkills: value ? [] : previous.childSkills,
                  }))
                }
              />
              <span className="memory-form-hint">{t("admin.memoryRootSkill")}</span>
            </div>
          ) : null}
          {!isChildSkillDraft ? (
              <div className="memory-form-field">
                <label>{t("admin.memoryCategory")}</label>
                <Input
                  value={draft.category}
                  readOnly={isReadOnly}
                  placeholder={t("admin.memoryCategoryPlaceholder")}
                  onChange={(event) =>
                    setDraft((previous: any) => ({ ...previous, category: event.target.value }))
                  }
                />
              </div>
          ) : null}
          {!isChildSkillDraft ? (
            <div className="memory-form-field">
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
              <span className="memory-form-hint">{t("admin.memoryTagsHint")}</span>
            </div>
          ) : null}
          {shouldShowSkillContentEditor ? (
            <div className="memory-form-field memory-form-field-full">
              <label>{t("admin.memoryMarkdown")}</label>
              <Input.TextArea
                rows={10}
                value={draft.content}
                readOnly={isReadOnly}
                placeholder={t("common.pleaseInput") + t("admin.memoryContent")}
                onChange={(event) =>
                  setDraft((previous: any) => ({ ...previous, content: event.target.value }))
                }
              />
              {activeTab === "skills" ? (
                <div className="memory-upload-actions">
                  <Upload {...createSkillUploadProps()} disabled={isReadOnly}>
                    <Button icon={<UploadOutlined />} disabled={isReadOnly}>
                      {t("admin.memoryUploadSkillFile")}
                    </Button>
                  </Upload>
                  <span className="memory-form-hint">
                    {t(
                      isChildSkillDraft
                        ? "admin.memoryUploadSkillFileHint"
                        : "admin.memoryUploadSkillFileHintParent",
                    )}
                  </span>
                </div>
              ) : null}
            </div>
          ) : null}
          {activeTab === "skills" && modalMode === "add" && !draft.parentId ? (
            <div className="memory-form-field memory-form-field-full memory-child-skill-section">
              <div className="memory-child-skill-header">
                <label>{t("admin.memoryChildSkillSection")}</label>
                <Button size="small" disabled={isReadOnly} onClick={addChildSkillDraft}>
                  {t("admin.memoryChildSkillAdd")}
                </Button>
              </div>
              {draft.childSkills.length ? (
                <div className="memory-child-skill-list">
                  {draft.childSkills.map((child: any, index: number) => (
                    <div key={child.tempId} className="memory-child-skill-card">
                      <div className="memory-child-skill-card-header">
                        <strong>{`${t("admin.memoryChildSkill")} ${index + 1}`}</strong>
                        <Button
                          type="text"
                          danger
                          size="small"
                          disabled={isReadOnly}
                          icon={<DeleteOutlined />}
                          onClick={() => removeChildSkillDraft(child.tempId)}
                        >
                          {t("admin.memoryChildSkillRemove")}
                        </Button>
                      </div>

                      <div className="memory-child-skill-grid">
                        <div className="memory-form-field">
                          <label>{t("admin.memoryName")}</label>
                          <Input
                            value={child.name}
                            readOnly={isReadOnly}
                            placeholder={t("common.pleaseInput") + t("admin.memoryName")}
                            onChange={(event) =>
                              updateChildSkillDraft(child.tempId, {
                                name: event.target.value,
                              })
                            }
                          />
                        </div>
                        <div className="memory-form-field memory-form-field-full">
                          <label>{t("admin.memoryDescription")}</label>
                          <Input.TextArea
                            rows={3}
                            autoSize={{ minRows: 3, maxRows: 6 }}
                            value={child.description}
                            readOnly={isReadOnly}
                            placeholder={t("common.pleaseInput") + t("admin.memoryDescription")}
                            onChange={(event) =>
                              updateChildSkillDraft(child.tempId, {
                                description: event.target.value,
                              })
                            }
                          />
                        </div>
                        <div className="memory-form-field memory-form-field-full">
                          <label>{t("admin.memoryMarkdown")}</label>
                          <Input.TextArea
                            rows={6}
                            value={child.content}
                            readOnly={isReadOnly}
                            placeholder={t("common.pleaseInput") + t("admin.memoryContent")}
                            onChange={(event) =>
                              updateChildSkillDraft(child.tempId, {
                                content: event.target.value,
                              })
                            }
                          />
                          <div className="memory-upload-actions">
                            <Upload
                              {...createSkillUploadProps(child.tempId)}
                              disabled={isReadOnly}
                            >
                              <Button icon={<UploadOutlined />} disabled={isReadOnly}>
                                {t("admin.memoryUploadSkillFile")}
                              </Button>
                            </Upload>
                            <span className="memory-form-hint">
                              {t("admin.memoryUploadSkillFileHint")}
                            </span>
                          </div>
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <span className="memory-form-hint">{t("admin.memoryChildSkillEmpty")}</span>
              )}
            </div>
          ) : null}
        </div>
      )}
    </Modal>
  );
}
