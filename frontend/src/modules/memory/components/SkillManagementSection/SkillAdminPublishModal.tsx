import { getLocalizedErrorMessage } from "@/components/request";
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Alert, Button, Input, Modal, Tabs, Upload, message } from "antd";
import { DeleteOutlined, InboxOutlined, PaperClipOutlined } from "@ant-design/icons";
import { publishSkillToMarket } from "../../skillApi";
import { uploadSkillTempFile } from "../../skillUpload";
import { isSkillPackageTooLargeError, isSkillSourceAmbiguousError } from "../../skillUploadError";
import { useChatInputStore } from "@/modules/chat/store/chatInput";
import { CHAT_HOME_PATH } from "@/modules/chat/constants/chat";

interface SkillAdminPublishModalProps {
  open: boolean;
  t: (key: string, options?: Record<string, unknown>) => string;
  onClose: () => void;
  onPublished: () => Promise<void>;
}

type PublishMethod = "url" | "file";

const MAX_SKILL_PACKAGE_SIZE = 512 * 1024 * 1024;

export default function SkillAdminPublishModal({
  open,
  t,
  onClose,
  onPublished,
}: SkillAdminPublishModalProps) {
  const navigate = useNavigate();
  const [publishMethod, setPublishMethod] = useState<PublishMethod>("url");
  const [repoUrl, setRepoUrl] = useState("");
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [urlImportError, setUrlImportError] = useState<unknown>(null);

  const resetForm = () => {
    setPublishMethod("url");
    setRepoUrl("");
    setSelectedFile(null);
    setUrlImportError(null);
  };

  const handleClose = () => {
    resetForm();
    onClose();
  };

  const handleClearFile = () => {
    setSelectedFile(null);
  };

  const installUrlInChat = () => {
    useChatInputStore.getState().setModeDraft({
      text: t("admin.memorySkillImportChatPrompt", { url: repoUrl.trim() }),
      mentions: [],
      files: [],
    });
    handleClose();
    navigate(CHAT_HOME_PATH);
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
    setUrlImportError(null);
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
        }, { silentError: true });
      }

      await onPublished();
      message.success(t("admin.memorySkillAdminPublishSuccessAuto"));
      handleClose();
    } catch (error) {
      console.error("Admin publish skill failed:", error);
      if (publishMethod === "url" && (isSkillPackageTooLargeError(error) || isSkillSourceAmbiguousError(error))) {
        setUrlImportError(error);
      } else if ((error as { response?: { status?: number } })?.response?.status === 409) {
        message.error(t("admin.memorySkillAdminPublishDuplicate"));
      } else {
        message.error(getLocalizedErrorMessage(error));
      }
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal
      open={open}
      title={t("admin.memorySkillAdminPublishTitle")}
      onCancel={() => {
        if (!submitting) handleClose();
      }}
      closable={!submitting}
      keyboard={!submitting}
      maskClosable={!submitting}
      footer={[
        <Button key="cancel" disabled={submitting} onClick={handleClose}>
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
          onChange={(key) => {
            setPublishMethod(key as PublishMethod);
            setUrlImportError(null);
          }}
          items={[
            {
              key: "url",
              disabled: submitting,
              label: t("admin.memorySkillAdminPublishMethodLink"),
              children: (
                <div className="memory-skill-field">
                  <label htmlFor="adminSkillUrlInput">{t("admin.memorySkillUploadRepoLabel")}</label>
                  <Input
                    id="adminSkillUrlInput"
                    aria-describedby={urlImportError ? "admin-skill-url-help admin-skill-url-error" : "admin-skill-url-help"}
                    aria-invalid={Boolean(urlImportError)}
                    disabled={submitting}
                    status={urlImportError ? "error" : undefined}
                    value={repoUrl}
                    maxLength={2048}
                    onChange={(event) => {
                      setRepoUrl(event.target.value);
                      setUrlImportError(null);
                    }}
                    placeholder={t("admin.memorySkillUploadRepoPlaceholder")}
                  />
                  <p id="admin-skill-url-help">{t("admin.memorySkillImportUrlHelp")}</p>
                  {Boolean(urlImportError) && (
                    <Alert
                      id="admin-skill-url-error"
                      type="error"
                      showIcon
                      message={t(isSkillPackageTooLargeError(urlImportError)
                        ? "admin.memorySkillImportTooLarge"
                        : "admin.memorySkillImportMultipleSkills")}
                      action={isSkillPackageTooLargeError(urlImportError) ? (
                        <Button onClick={installUrlInChat}>
                          {t("admin.memorySkillImportGoToChat")}
                        </Button>
                      ) : undefined}
                    />
                  )}
                </div>
              ),
            },
            {
              key: "file",
              disabled: submitting,
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
