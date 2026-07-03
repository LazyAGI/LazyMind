import { useState } from "react";
import { Button, Input, Tooltip, Upload, message } from "antd";
import { InboxOutlined, QuestionCircleOutlined } from "@ant-design/icons";
import { getLocalizedErrorMessage } from "@/components/request";
import {
  canUploadSkillFile,
  getBaseName,
  parseMarkdownFrontMatter,
} from "../../shared";
import { createSkillAsset } from "../../skillApi";

interface SkillUploadViewProps {
  t: (key: string, options?: Record<string, unknown>) => string;
  onUploaded: () => Promise<void>;
  onNavigateInstalled: () => void;
}

const readFileAsText = (file: File) =>
  new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ""));
    reader.onerror = () => reject(reader.error);
    reader.readAsText(file);
  });

export default function SkillUploadView({
  t,
  onUploaded,
  onNavigateInstalled,
}: SkillUploadViewProps) {
  const [repoUrl, setRepoUrl] = useState("");
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const resetForm = () => {
    setRepoUrl("");
    setSelectedFile(null);
  };

  const handleSubmit = async () => {
    if (!repoUrl.trim() && !selectedFile) {
      message.warning(t("admin.memorySkillUploadMissing"));
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
        description = frontMatter?.description || t("admin.memorySkillUploadPersonalDesc");
        content = frontMatter?.content ?? rawContent;
      } else {
        const rawName = repoUrl.split("/").filter(Boolean).pop() || "";
        name = rawName.replace(/[-_]/g, " ") || t("admin.memorySkillUploadDefaultName");
        description = t("admin.memorySkillUploadPersonalDesc");
        content = `# ${name}\n\n${t("admin.memorySkillUploadUrlPlaceholderContent")}\n\nSource: ${repoUrl.trim()}`;
      }

      await createSkillAsset({
        name,
        description,
        category: "personal",
        tags: [],
        content,
        file_ext: "md",
        is_enabled: true,
      });

      await onUploaded();
      resetForm();
      onNavigateInstalled();
      message.success(t("admin.memorySkillUploadSuccess", { name }));
    } catch (error) {
      console.error("Upload skill failed:", error);
      message.error(
        getLocalizedErrorMessage(error, t("admin.memorySkillUploadFailed")) ||
          t("admin.memorySkillUploadFailed"),
      );
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="memory-skill-upload-view">
      <div className="memory-skill-upload-head">
        <div className="memory-skill-with-help">
          <h2>{t("admin.memorySkillUploadTitle")}</h2>
          <Tooltip title={t("admin.memorySkillUploadHelp")}>
            <button
              type="button"
              className="memory-skill-tooltip-help"
              aria-label={t("admin.memorySkillUploadHelp")}
            >
              <QuestionCircleOutlined />
            </button>
          </Tooltip>
        </div>
      </div>

      <section className="memory-skill-upload-panel">
        <h3 className="memory-skill-with-help">
          <span>{t("admin.memorySkillUploadSourceTitle")}</span>
          <Tooltip title={t("admin.memorySkillUploadSourceHelp")}>
            <button
              type="button"
              className="memory-skill-tooltip-help"
              aria-label={t("admin.memorySkillUploadSourceHelp")}
            >
              <QuestionCircleOutlined />
            </button>
          </Tooltip>
        </h3>

        <div className="memory-skill-upload-form">
          <div className="memory-skill-field">
            <label htmlFor="skillUrlInput">{t("admin.memorySkillUploadRepoLabel")}</label>
            <Input
              id="skillUrlInput"
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
              <strong>
                {selectedFile?.name || t("admin.memorySkillUploadFileTitle")}
              </strong>
            </p>
            <p className="ant-upload-hint">
              {selectedFile
                ? t("admin.memorySkillUploadFileReady", {
                    size: Math.max(1, Math.round(selectedFile.size / 1024)),
                  })
                : t("admin.memorySkillUploadFileHint")}
            </p>
          </Upload.Dragger>
        </div>

        <div className="memory-skill-upload-actions">
          <Button onClick={resetForm}>{t("admin.memorySkillUploadClear")}</Button>
          <Button type="primary" loading={submitting} onClick={() => void handleSubmit()}>
            {t("admin.memorySkillUploadSubmit")}
          </Button>
        </div>
      </section>
    </div>
  );
}
