import { useState } from "react";
import { Button, Input, Modal, Upload, message } from "antd";
import { InboxOutlined } from "@ant-design/icons";
import { getLocalizedErrorMessage } from "@/components/request";
import {
  canUploadSkillFile,
  getBaseName,
  parseMarkdownFrontMatter,
} from "../../shared";
import { createSkillAsset } from "../../skillApi";

interface SkillAdminPublishModalProps {
  open: boolean;
  t: (key: string, options?: Record<string, unknown>) => string;
  onClose: () => void;
  onPublished: () => Promise<void>;
}

const readFileAsText = (file: File) =>
  new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ""));
    reader.onerror = () => reject(reader.error);
    reader.readAsText(file);
  });

export default function SkillAdminPublishModal({
  open,
  t,
  onClose,
  onPublished,
}: SkillAdminPublishModalProps) {
  const [repoUrl, setRepoUrl] = useState("");
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const resetForm = () => {
    setRepoUrl("");
    setSelectedFile(null);
  };

  const handleClose = () => {
    resetForm();
    onClose();
  };

  const handleSubmit = async () => {
    if (!repoUrl.trim() && !selectedFile) {
      message.warning(t("admin.memorySkillAdminPublishMissing"));
      return;
    }

    setSubmitting(true);
    try {
      let name = "";
      let description = "";
      let content = "";

      if (selectedFile) {
        if (!canUploadSkillFile(selectedFile.name, true)) {
          message.warning(t("admin.memoryUploadSkillTypeInvalidParent"));
          return;
        }
        const rawContent = await readFileAsText(selectedFile);
        const frontMatter = parseMarkdownFrontMatter(rawContent);
        name = frontMatter?.name || getBaseName(selectedFile.name);
        description = frontMatter?.description || t("admin.memorySkillAdminPublishDefaultDesc");
        content = frontMatter?.content ?? rawContent;
      } else {
        const rawName = repoUrl.split("/").filter(Boolean).pop() || "";
        name = rawName.replace(/[-_]/g, " ") || t("admin.memorySkillAdminPublishDefaultName");
        description = t("admin.memorySkillAdminPublishDefaultDesc");
        content = `# ${name}\n\n${t("admin.memorySkillAdminPublishUrlPlaceholderContent")}\n\nSource: ${repoUrl.trim()}`;
      }

      await createSkillAsset({
        name,
        description,
        category: "team",
        tags: [],
        content,
        file_ext: "md",
        is_enabled: true,
      });

      await onPublished();
      message.success(t("admin.memorySkillAdminPublishSuccess", { name }));
      handleClose();
    } catch (error) {
      console.error("Admin publish skill failed:", error);
      message.error(
        getLocalizedErrorMessage(error, t("admin.memorySkillAdminPublishFailed")) ||
          t("admin.memorySkillAdminPublishFailed"),
      );
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
        <div className="memory-skill-field">
          <label htmlFor="adminSkillUrlInput">{t("admin.memorySkillUploadRepoLabel")}</label>
          <Input
            id="adminSkillUrlInput"
            value={repoUrl}
            onChange={(event) => setRepoUrl(event.target.value)}
            placeholder={t("admin.memorySkillUploadRepoPlaceholder")}
          />
        </div>
        <Upload.Dragger
          accept=".md,.markdown,.zip,.tgz,.tar,.gz"
          multiple={false}
          showUploadList={Boolean(selectedFile)}
          beforeUpload={(file) => {
            setSelectedFile(file);
            return false;
          }}
          onRemove={() => {
            setSelectedFile(null);
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
      </div>
    </Modal>
  );
}
