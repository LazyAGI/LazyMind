import { useState } from "react";
import { Button, Input, Modal, Tabs, Upload, message } from "antd";
import { DeleteOutlined, InboxOutlined, PaperClipOutlined } from "@ant-design/icons";
import { publishSkillToMarket } from "../../skillApi";
import { uploadSkillTempFile } from "../../skillUpload";

interface SkillAdminPublishModalProps {
  open: boolean;
  t: (key: string, options?: Record<string, unknown>) => string;
  onClose: () => void;
  onPublished: () => Promise<void>;
}

type PublishMethod = "url" | "file";

const MAX_SKILL_PACKAGE_SIZE = 512 * 1024 * 1024;

const normalizeRepositoryArchiveUrl = (rawUrl: string) => {
  const value = rawUrl.trim();
  try {
    const url = new URL(value);
    if (url.hostname.toLowerCase() !== "github.com") {
      return value;
    }
    const pathParts = url.pathname.split("/").filter(Boolean);
    if (pathParts.length !== 2) {
      return value;
    }
    const [owner, rawRepository] = pathParts;
    const repository = rawRepository.replace(/\.git$/i, "");
    if (!owner || !repository) {
      return value;
    }
    return `https://github.com/${owner}/${repository}/archive/HEAD.zip`;
  } catch {
    return value;
  }
};

export default function SkillAdminPublishModal({
  open,
  t,
  onClose,
  onPublished,
}: SkillAdminPublishModalProps) {
  const [publishMethod, setPublishMethod] = useState<PublishMethod>("url");
  const [repoUrl, setRepoUrl] = useState("");
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const resetForm = () => {
    setPublishMethod("url");
    setRepoUrl("");
    setSelectedFile(null);
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

    const normalizedRepoUrl = normalizeRepositoryArchiveUrl(repoUrl);
    if (publishMethod === "url" && !normalizedRepoUrl) {
      message.warning(t("admin.memorySkillAdminPublishUrlMissing"));
      return;
    }
    if (publishMethod === "file" && !selectedFile) {
      message.warning(t("admin.memorySkillAdminPublishFileMissing"));
      return;
    }
    setSubmitting(true);
    try {
      if (publishMethod === "file" && selectedFile) {
        const upload = await uploadSkillTempFile(selectedFile);
        await publishSkillToMarket({
          source: { type: "uploaded_zip", uploadId: upload.uploadId },
        });
      } else {
        await publishSkillToMarket({
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
      </div>
    </Modal>
  );
}
