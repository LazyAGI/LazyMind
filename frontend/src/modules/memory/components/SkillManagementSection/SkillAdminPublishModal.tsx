import { useState } from "react";
import { Button, Input, Modal, Upload, message } from "antd";
import { InboxOutlined } from "@ant-design/icons";
import { getLocalizedErrorMessage } from "@/components/request";
import { createSkillAsset } from "../../skillApi";
import { uploadSkillTempFile } from "../../skillUpload";

interface SkillAdminPublishModalProps {
  open: boolean;
  t: (key: string, options?: Record<string, unknown>) => string;
  onClose: () => void;
  onPublished: () => Promise<void>;
}

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
      const name =
        selectedFile?.name.replace(/\.(zip|tgz|tar|gz)$/i, "") ||
        repoUrl.split("/").filter(Boolean).pop()?.replace(/[-_]/g, " ") ||
        t("admin.memorySkillAdminPublishDefaultName");

      if (selectedFile) {
        const upload = await uploadSkillTempFile(selectedFile);
        await createSkillAsset({
          name,
          description: t("admin.memorySkillAdminPublishDefaultDesc"),
          category: "team",
          tags: [],
          isEnabled: true,
          source: { type: "uploaded_zip", uploadId: upload.uploadId },
        });
      } else {
        await createSkillAsset({
          name,
          description: t("admin.memorySkillAdminPublishDefaultDesc"),
          category: "team",
          tags: [],
          isEnabled: true,
          source: { type: "url", url: repoUrl.trim() },
        });
      }

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
          accept=".zip,.tgz,.tar,.gz"
          multiple={false}
          showUploadList={Boolean(selectedFile)}
          beforeUpload={(file) => {
            setSelectedFile(file);
            setRepoUrl("");
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
