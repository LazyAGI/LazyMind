import { getLocalizedErrorMessage } from "@/components/request";
import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Alert, Button, Input, Modal, Tabs, Upload, message } from "antd";
import { DeleteOutlined, InboxOutlined, PaperClipOutlined } from "@ant-design/icons";
import { publishSkillToMarket } from "../../skillApi";
import { abortSkillTempUpload, getSkillPackageValidationError, isInvalidSkillPackageError, uploadSkillTempFile } from "../../skillUpload";
import { useSkillImportTask } from "../../hooks/useSkillImportTask";
import SkillImportProgress from "./SkillImportProgress";
import { isSkillAlreadyExistsError, isSkillPackageTooLargeError, isSkillSourceAmbiguousError } from "../../skillUploadError";
import { useChatInputStore } from "@/modules/chat/store/chatInput";
import { CHAT_HOME_PATH } from "@/modules/chat/constants/chat";

interface SkillAdminPublishModalProps {
  open: boolean;
  t: (key: string, options?: Record<string, unknown>) => string;
  onClose: () => void;
  onPublished: () => Promise<void>;
}

type PublishMethod = "url" | "file";

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
  const [urlImportError, setUrlImportError] = useState<unknown>(null);
  const getImportErrorMessage = (error: unknown) => isSkillPackageTooLargeError(error)
    ? t("admin.memorySkillImportTooLarge")
    : isSkillSourceAmbiguousError(error)
      ? t("admin.memorySkillImportMultipleSkills")
      : isInvalidSkillPackageError(error)
        ? t("admin.memorySkillUploadInvalidPackage")
      : isSkillAlreadyExistsError(error)
        ? t("admin.memorySkillAdminPublishDuplicate")
        : getLocalizedErrorMessage(error);
  const { pending: submitting, uploadProgress, start, cancel } = useSkillImportTask({
    onSuccess: () => {
      message.success(t("admin.memorySkillAdminPublishSuccessAuto"));
      handleClose();
      // Catalog refresh is independent of the completed publish operation.
      void Promise.resolve().then(onPublished).catch((error) => {
        console.error("Refresh published skills failed:", error);
      });
    },
    onError: (error) => {
      console.error("Admin publish skill failed:", error);
      if (publishMethod === "url") setUrlImportError(error);
      else message.error(getImportErrorMessage(error));
    },
  });
  useEffect(() => {
    if (!open) cancel();
  }, [open, cancel]);


  const resetForm = () => {
    setPublishMethod("url");
    setRepoUrl("");
    setSelectedFile(null);
    setUrlImportError(null);
  };

  const handleClose = () => {
    cancel();
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
    await start(async ({ signal, setUploadProgress }) => {
      if (publishMethod === "file" && selectedFile) {
        let uploadId = "";
        try {
          const upload = await uploadSkillTempFile(selectedFile, { validatePackage: true, signal, onProgress: setUploadProgress, silentError: true });
          uploadId = upload.uploadId;
          signal.throwIfAborted();
          setUploadProgress(null);
          await publishSkillToMarket({
            source: { type: "uploaded_zip", uploadId },
          }, { signal, silentError: true });
        } catch (error) {
          if (uploadId) await abortSkillTempUpload(uploadId);
          throw error;
        }
      } else {
        await publishSkillToMarket({
          source: { type: "url", url: normalizedRepoUrl },
        }, { signal, silentError: true });
      }
    });
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
        <Button key="submit" type="primary" disabled={submitting} loading={submitting} onClick={() => void handleSubmit()}>
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
                      message={getImportErrorMessage(urlImportError)}
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
                    accept=".zip"
                    disabled={submitting}
                    multiple={false}
                    showUploadList={false}
                    beforeUpload={(file) => {
                      const validationError = getSkillPackageValidationError(file);
                      if (validationError) {
                        message.warning(t(validationError === "type"
                          ? "admin.memorySkillUploadPackageTypeError"
                          : "admin.memorySkillUploadPackageSizeError"));
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
                        disabled={submitting}
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
      {submitting ? <SkillImportProgress t={t} uploadProgress={uploadProgress} fileName={selectedFile?.name} /> : null}
    </Modal>
  );
}
