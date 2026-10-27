import { Alert, Button, Progress } from "antd";
import { LoadingOutlined } from "@ant-design/icons";

export default function SkillImportProgress({
  t,
  uploadProgress,
  fileName,
  onCancel,
}: {
  t: (key: string, options?: Record<string, unknown>) => string;
  uploadProgress: number | null;
  fileName?: string;
  onCancel?: () => void;
}) {
  return (
    <Alert
      type="info"
      role="status"
      aria-live="polite"
      showIcon
      icon={<LoadingOutlined spin aria-hidden="true" />}
      message={uploadProgress === null
        ? t("admin.memorySkillImportPending")
        : t("admin.memorySkillUploadProgress", { percent: uploadProgress })}
      description={<>
        {fileName ? <div style={{ overflowWrap: "anywhere" }}>{fileName}</div> : null}
        {uploadProgress !== null ? <Progress aria-label={t("admin.memorySkillUploadSubmit")} percent={uploadProgress} size="small" showInfo={false} /> : null}
        <span>{t("admin.memorySkillImportPendingHint")}</span>
      </>}
      action={onCancel ? <Button onClick={onCancel}>{t("common.cancel")}</Button> : undefined}
      style={{ marginTop: 12, flex: "0 0 auto" }}
    />
  );
}
