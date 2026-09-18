import {
  CheckCircleFilled,
  CloudServerOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { Button, Skeleton, Tag } from "antd";
import { useCloudSystemProviderCard, type CloudSystemProviderModel } from "../hooks/useCloudSystemProviderCard";
export type { CloudSystemProviderModel } from "../hooks/useCloudSystemProviderCard";

export interface CloudSystemProviderCardProps {
  state: "loading" | "ready" | "signed_out" | "plan_required" | "error";
  models: CloudSystemProviderModel[];
  onLogin: () => void;
  onOpenPlan: () => void;
  onRetry: () => void;
}

export default function CloudSystemProviderCard(
  {
    state,
    models,
    onLogin,
    onOpenPlan,
    onRetry,
  }: CloudSystemProviderCardProps,
) {
  const { t, availableCount, displayModels } = useCloudSystemProviderCard(models);

  return (
    <article
      className="model-provider-cloud-system-card"
      data-provider-key="lazymind-cloud"
      aria-labelledby="model-provider-cloud-system-title"
    >
      <div className="model-provider-cloud-system-summary">
        <span className="model-provider-cloud-system-icon" aria-hidden="true">
          <CloudServerOutlined />
        </span>
        <div className="model-provider-cloud-system-copy">
          <div className="model-provider-cloud-system-title-row">
            <strong id="model-provider-cloud-system-title">LazyMind Cloud</strong>
            <Tag className="model-provider-cloud-system-tag">
              {t("modelProvider.cloudSystemBadge")}
            </Tag>
          </div>
          <span>{t("modelProvider.cloudSystemReadOnly")}</span>
        </div>
        {state === "ready" ? (
          <span className="model-provider-cloud-system-ready">
            <CheckCircleFilled aria-hidden="true" />
            {t("modelProvider.cloudSystemAvailableCount", {
              count: availableCount,
            })}
          </span>
        ) : null}
      </div>

      {state === "loading" ? (
        <Skeleton active title={false} paragraph={{ rows: 2 }} />
      ) : null}

      {state === "signed_out" ? (
        <div className="model-provider-cloud-system-state" role="status">
          <span>{t("modelProvider.cloudSystemSignedOut")}</span>
          <Button type="primary" onClick={onLogin}>
            {t("modelProvider.cloudSystemLogin")}
          </Button>
        </div>
      ) : null}

      {state === "plan_required" ? (
        <div className="model-provider-cloud-system-state" role="status">
          <span>{t("modelProvider.cloudSystemPlanRequired")}</span>
          <Button type="primary" onClick={onOpenPlan}>
            {t("modelProvider.cloudSystemOpenPlan")}
          </Button>
        </div>
      ) : null}

      {state === "error" ? (
        <div className="model-provider-cloud-system-state" role="alert">
          <span>{t("modelProvider.cloudSystemUnavailable")}</span>
          <Button icon={<ReloadOutlined aria-hidden="true" />} onClick={onRetry}>
            {t("common.retry")}
          </Button>
        </div>
      ) : null}

      {state === "ready" ? (
        <div
          className="model-provider-cloud-system-models"
          aria-label={t("modelProvider.cloudSystemModelsAria")}
        >
          {displayModels.length ? (
            displayModels.map((model) => (
              <div className="model-provider-cloud-system-model" key={model.id}>
                <span title={model.name}>{model.name}</span>
                <Tag>{model.modelTypeLabel}</Tag>
                {model.availability === "degraded" ? (
                  <Tag color="warning">
                    {t("modelProvider.cloudSystemDegraded")}
                  </Tag>
                ) : null}
                {model.lifecycle === "deprecated" ? (
                  <Tag>{t("modelProvider.cloudSystemDeprecated")}</Tag>
                ) : model.lifecycle === "retired" ? (
                  <Tag>{t("modelProvider.cloudSystemRetired")}</Tag>
                ) : null}
              </div>
            ))
          ) : (
            <span className="model-provider-cloud-system-empty">
              {t("modelProvider.cloudSystemNoModels")}
            </span>
          )}
        </div>
      ) : null}
    </article>
  );
}
