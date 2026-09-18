import { useId } from "react";
import { Button, Select, Typography } from "antd";
import { ArrowRightOutlined, InfoCircleOutlined, ReloadOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import type { EvolutionModels } from "../shared/evolutionModels";

export function EvolutionModelSelect({ catalog, loading, error, value, onChange, onRetry }: {
  catalog?: EvolutionModels; loading: boolean; error: string; value?: string;
  onChange: (value: string) => void; onRetry: () => void;
}) {
  const { t } = useTranslation();
  const statusId = useId();
  const selectedModel = catalog?.models.find(model => model.model_ref === value);
  const reason = catalog?.unavailable_reason || "";
  const knownReason = ["not_configured", "unavailable", "incompatible", "connection_unverified", "configuration_incomplete", "credentials_unavailable"].includes(reason);
  const statusMessage = error || (!loading && catalog && (
    !catalog.models.length || reason
      ? t(knownReason ? `selfEvolutionControls.unavailable.${reason}`
        : catalog.models.length ? "selfEvolutionControls.defaultUnavailable" : "selfEvolutionControls.noModels")
      : ""
  ));

  return (
    <div className="self-evolution-model-select" aria-busy={loading}>
      <div className="self-evolution-model-control-row">
        <Select
          aria-label={t("selfEvolutionControls.model")}
          aria-describedby={statusMessage ? statusId : undefined}
          value={value}
          loading={loading}
          disabled={loading || !catalog?.can_select}
          status={error ? "error" : undefined}
          onChange={onChange}
          placeholder={t(loading
            ? "selfEvolutionControls.loadingModels"
            : catalog && !catalog.models.length
              ? "selfEvolutionControls.noModelsPlaceholder"
              : "selfEvolutionControls.selectModel")}
          options={catalog?.models.map(model => ({
            value: model.model_ref,
            label: `${model.display_name} · ${t(`selfEvolutionControls.source.${model.source}`)}${model.model_ref === catalog.available_default_ref ? ` · ${t("selfEvolutionControls.recommended")}` : ""}`,
          })) || []}
        />
        <Button
          className="self-evolution-model-refresh"
          icon={<ReloadOutlined aria-hidden />}
          onClick={onRetry}
          loading={loading}
          disabled={loading}
        >
          {t("selfEvolutionControls.refreshModels")}
        </Button>
      </div>
      {catalog?.configured_default && (
        <Typography.Text className="self-evolution-model-default">
          {t("selfEvolutionControls.configuredDefault", {
            model: catalog.configured_default.display_name,
            source: t(`selfEvolutionControls.source.${catalog.configured_default.source}`),
          })}
        </Typography.Text>
      )}
      {selectedModel && (
        <Typography.Text className="self-evolution-model-default">
          {t(`selfEvolutionControls.validation.${selectedModel.validation_status || "unverified"}`)}
        </Typography.Text>
      )}
      {statusMessage && (
        <div className={`self-evolution-model-notice${error ? " is-error" : ""}`}>
          <InfoCircleOutlined aria-hidden />
          <span id={statusId} role="status">{statusMessage}</span>
          {(!catalog?.models.length || error) && (
            <Link to="/settings?section=models" className="self-evolution-model-configure">
              {t("selfEvolutionControls.configureModels")}
              <ArrowRightOutlined aria-hidden />
            </Link>
          )}
        </div>
      )}
    </div>
  );
}
