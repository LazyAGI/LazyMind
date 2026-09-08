import { useEffect, useState } from "react";
import { Alert, Button, Select, Space, Spin } from "antd";
import { useTranslation } from "react-i18next";
import type { useForkConversation } from "./useForkConversation";

export default function ForkStatus({ fork, source }: { fork: ReturnType<typeof useForkConversation>; source: string }) {
  const { t } = useTranslation();
  const [model, setModel] = useState<string>();
  const [models, setModels] = useState<{ label: string; value: string }[]>([]);
  const [modelError, setModelError] = useState(false);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [modelAttempt, setModelAttempt] = useState(0);
  const needsModel = fork.phase === "ready" && fork.preview?.config_issues.some((issue) => issue.field === "model");

  useEffect(() => {
    let active = true;
    setModel(undefined);
    if (!needsModel) return;
    setModels([]); setModelError(false); setModelsLoading(true);
    import("../ChatModelSelector/api")
      .then(({ fetchChatModelCatalog }) => fetchChatModelCatalog(source))
      .then((response) => {
        if (!active) return;
        setModels((response.providers || []).flatMap((provider) => (provider.models || [])
          .filter((item) => item.availability === "available" && item.id)
          .map((item) => ({ label: `${provider.name} · ${item.name}`, value: item.id! }))));
      })
      .catch(() => { if (active) setModelError(true); })
      .finally(() => { if (active) setModelsLoading(false); });
    return () => { active = false; };
  }, [needsModel, source, modelAttempt, fork.preview]);

  return <div aria-live="polite">
    {fork.pending && <Space role="status" style={{ marginBottom: 8 }}>
      <Spin size="small" />{t("chat.fork.creating")}
    </Space>}
    {!fork.pending && fork.recoverable.map((operation) =>
      <Alert key={operation.id} type="info" showIcon style={{ marginBottom: 8 }}
        message={t("chat.fork.unknown")}
        description={t("chat.fork.unknownDescription")}
        action={<Button size="small" onClick={() => fork.resume(operation)}>{t("chat.fork.retry")}</Button>} />)}
    {!fork.pending && (fork.phase === "preview_error" || fork.phase === "failed") && <Alert
      type="error" showIcon closable onClose={fork.dismissStatus} style={{ marginBottom: 8 }}
      message={t(`chat.fork.errors.${fork.error}`, { defaultValue: t("chat.fork.errors.FORK_FAILED") })}
      action={fork.error !== "PENDING_FORK" && <Button size="small" onClick={fork.retry}>{t("chat.fork.retryCreate")}</Button>} />}
    {needsModel && <Alert type="warning" showIcon style={{ marginBottom: 8 }}
      message={t("chat.fork.errors.MODEL_UNAVAILABLE")}
      description={<Space direction="vertical" style={{ width: "100%" }}>
        <label htmlFor="fork-model">{t("chat.fork.chooseModel")}</label>
        <Select id="fork-model" style={{ width: "100%" }} value={model} onChange={setModel}
          options={models} loading={modelsLoading} disabled={fork.pending}
          placeholder={t("chat.fork.chooseModel")} />
        {(modelError || (!modelsLoading && models.length === 0)) && <div role="status">
          {t(modelError ? "chat.fork.modelLoadFailed" : "chat.fork.noModels")}
          <Button size="small" onClick={() => setModelAttempt((value) => value + 1)}>{t("chat.fork.retryRead")}</Button>
        </div>}
        <Space>
          <Button onClick={fork.dismissStatus}>{t("common.cancel")}</Button>
          <Button type="primary" disabled={!model || fork.pending} onClick={() => model && fork.selectModel(model)}>{t("chat.fork.create")}</Button>
        </Space>
      </Space>} />}
  </div>;
}
