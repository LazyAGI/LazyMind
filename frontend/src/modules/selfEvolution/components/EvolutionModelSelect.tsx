import { useId, type ReactNode } from "react";
import { Select, Typography } from "antd";
import { ArrowRightOutlined, InfoCircleOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import type { EvolutionModels } from "../shared/evolutionModels";

const renderModelLabel = ({ label }: { label?: ReactNode }) => (
  <Typography.Text className="self-evolution-model-label" ellipsis={{ tooltip: true }}>
    {label}
  </Typography.Text>
);

export function EvolutionModelSelect({ catalog, loading, error, value, onChange }: {
  catalog?: EvolutionModels; loading: boolean; error: string; value?: string;
  onChange: (value: string) => void;
}) {
  const { t } = useTranslation();
  const statusId = useId();
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
      <Select
        aria-label={t("selfEvolutionControls.model")}
        aria-describedby={statusMessage ? statusId : undefined}
        value={value}
        loading={loading}
        disabled={loading || !catalog?.can_select}
        status={error ? "error" : undefined}
        onChange={onChange}
        labelRender={renderModelLabel}
        optionRender={renderModelLabel}
        placeholder={t(loading
          ? "selfEvolutionControls.loadingModels"
          : catalog && !catalog.models.length
            ? "selfEvolutionControls.noModelsPlaceholder"
            : "selfEvolutionControls.selectModel")}
        options={catalog?.models.map(model => ({
          value: model.model_ref,
          title: "",
          label: `${model.display_name} · ${t(`selfEvolutionControls.source.${model.source}`)}${model.model_ref === catalog.available_default_ref ? ` · ${t("selfEvolutionControls.recommended")}` : ""}`,
        })) || []}
      />
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
