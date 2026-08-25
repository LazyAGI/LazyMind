import { useState } from "react";
import { Button, Input, Modal, Select, Tabs, Upload, message } from "antd";
import { DeleteOutlined, InboxOutlined, PaperClipOutlined } from "@ant-design/icons";
import { publishSkillToMarket } from "../../skillApi";
import { uploadSkillTempFile } from "../../skillUpload";
import { SKILL_TAG_MAX_COUNT } from "../../shared";

interface SkillAdminPublishModalProps {
  open: boolean;
  t: (key: string, options?: Record<string, unknown>) => string;
  onClose: () => void;
  onPublished: () => Promise<void>;
  tagOptions: string[];
  tagsLoading: boolean;
}

type PublishMethod = "url" | "file";

const MAX_SKILL_PACKAGE_SIZE = 512 * 1024 * 1024;

export default function SkillAdminPublishModal({
  open,
  t,
  onClose,
  onPublished,
  tagOptions,
  tagsLoading,
}: SkillAdminPublishModalProps) {
  const [publishMethod, setPublishMethod] = useState<PublishMethod>("url");
  const [repoUrl, setRepoUrl] = useState("");
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [tags, setTags] = useState<string[]>([]);
  const [submitting, setSubmitting] = useState(false);

  const resetForm = () => {
    setPublishMethod("url");
    setRepoUrl("");
    setSelectedFile(null);
    setTags([]);
  };

  const handleClose = () => {
    resetForm();
    onClose();
  };

  const handleClearFile = () => {
    setSelectedFile(null);
  };

  const handleSubmit = async () => {
    if (submitting) {
      return;
    }

    const normalizedRepoUrl = repoUrl.trim();
    if (publishMethod === "url" && !normalizedRepoUrl) {
      message.warning(t("admin.memorySkillAdminPublishUrlMissing"));
      return;
    }
    if (publishMethod === "file" && !selectedFile) {
      message.warning(t("admin.memorySkillAdminPublishFileMissing"));
      return;
    }
    if (tags.length === 0) {
      message.warning(t("admin.memorySkillAdminPublishTagsMissing"));
      return;
    }

    setSubmitting(true);
    try {
      if (publishMethod === "file" && selectedFile) {
        const upload = await uploadSkillTempFile(selectedFile);
        await publishSkillToMarket({
          tags,
          source: { type: "uploaded_zip", uploadId: upload.uploadId },
        });
      } else {
        await publishSkillToMarket({
          tags,
          source: { type: "url", url: normalizedRepoUrl },
        });
      }

      await onPublished();
      message.success(t("admin.memorySkillAdminPublishSuccessAuto"));
      handleClose();
    } catch (error) {
      console.error("Admin publish skill failed:", error);
      if ((error as { response?: { status?: number } })?.response?.status === 409) {
        message.error(t("admin.memorySkillAdminPublishDuplicate"));
      } else {
        message.error(t("admin.memorySkillAdminPublishFailed"));
      }
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal
      open={open}
      title={t("admin.memorySkillAdminPublishTitle")}
      onCancel={handleClose}
      footer={[
        <Button key="cancel" onClick={handleClose}>
          {t("common.cancel")}
        </Button>,
        <Button key="submit" type="primary" loading={submitting} onClick={() => void handleSubmit()}>
          {t("admin.memorySkillAdminPublishSubmit")}
        </Button>,
      ]}
      width={640}
      destroyOnClose
    >
      <p className="memory-skill-admin-publish-desc">{t("admin.memorySkillAdminPublishDesc")}</p>
      <div className="memory-skill-admin-publish-form">
        <Tabs
          activeKey={publishMethod}
          onChange={(key) => setPublishMethod(key as PublishMethod)}
          items={[
            {
              key: "url",
              label: t("admin.memorySkillAdminPublishMethodLink"),
              children: (
                <div className="memory-skill-field">
                  <label htmlFor="adminSkillUrlInput">{t("admin.memorySkillUploadRepoLabel")}</label>
                  <Input
                    id="adminSkillUrlInput"
                    value={repoUrl}
                    maxLength={2048}
                    onChange={(event) => setRepoUrl(event.target.value)}
                    placeholder={t("admin.memorySkillUploadRepoPlaceholder")}
                  />
                </div>
              ),
            },
            {
              key: "file",
              label: t("admin.memorySkillAdminPublishMethodPackage"),
              children: (
                <div className="memory-skill-admin-package-panel">
                  <Upload.Dragger
                    accept=".zip,.tgz,.tar,.gz"
                    multiple={false}
                    showUploadList={false}
                    beforeUpload={(file) => {
                      const name = file.name.toLowerCase();
                      const valid =
                        name.endsWith(".zip") ||
                        name.endsWith(".tgz") ||
                        name.endsWith(".tar") ||
                        name.endsWith(".gz");
                      if (!valid) {
                        message.warning(t("admin.memorySkillUploadPackageTypeError"));
                        return false;
                      }
                      if (file.size > MAX_SKILL_PACKAGE_SIZE) {
                        message.warning(t("admin.memorySkillUploadPackageSizeError"));
                        return false;
                      }
                      setSelectedFile(file);
                      return false;
                    }}
                    className="memory-skill-file-drop"
                  >
                    <p className="ant-upload-drag-icon">
                      <InboxOutlined />
                    </p>
                    <p className="ant-upload-text">
                      <strong>{t("admin.memorySkillAdminPublishFileTitle")}</strong>
                    </p>
                    <p className="ant-upload-hint">{t("admin.memorySkillUploadFileHint")}</p>
                  </Upload.Dragger>
                  {selectedFile ? (
                    <div className="memory-skill-selected-file">
                      <span className="memory-skill-selected-file-name">
                        <PaperClipOutlined />
                        <span title={selectedFile.name}>{selectedFile.name}</span>
                      </span>
                      <Button
                        type="text"
                        size="small"
                        danger
                        icon={<DeleteOutlined />}
                        aria-label={t("common.delete")}
                        onClick={handleClearFile}
                      />
                    </div>
                  ) : null}
                </div>
              ),
            },
          ]}
        />
        <div className="memory-skill-field">
          <label htmlFor="adminSkillTagsInput">{t("admin.memorySkillMarketTags")}</label>
          <Select
            id="adminSkillTagsInput"
            mode="tags"
            allowClear
            showSearch
            loading={tagsLoading}
            value={tags}
            options={tagOptions.map((tag) => ({ label: tag, value: tag }))}
            placeholder={t("admin.memoryTagsPlaceholder")}
            tokenSeparators={[",", "，"]}
            onChange={(values: string[]) => {
              const normalized = [
                ...new Set(values.map((tag) => tag.trim()).filter(Boolean)),
              ];
              if (normalized.length > SKILL_TAG_MAX_COUNT) {
                message.warning(
                  t("admin.memorySkillTagMaxCount", {
                    count: SKILL_TAG_MAX_COUNT,
                  }),
                );
              }
              setTags(normalized.slice(0, SKILL_TAG_MAX_COUNT));
            }}
          />
        </div>
      </div>
    </Modal>
  );
}
