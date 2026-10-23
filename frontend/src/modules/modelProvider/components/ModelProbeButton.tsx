import { useEffect, useRef, useState } from "react";
import { ApiOutlined, CheckCircleOutlined, ClockCircleOutlined, CloseCircleOutlined, MinusCircleOutlined } from "@ant-design/icons";
import { Button, Tooltip } from "antd";
import { useTranslation } from "react-i18next";
import { probeFailureCode, probeModel, type ModelProbeIdentity, type ModelProbeResult } from "../modelProbe";
import "./ModelProbeButton.scss";

interface ModelProbeButtonProps extends ModelProbeIdentity {
  modelName: string;
  configurationKey: string;
  disabledReason?: string;
  disabled?: boolean;
  onPendingChange?: (pending: boolean) => void;
}

/** Ephemeral inference health only; it never writes provider verification or selection. */
export function ModelProbeButton(props: ModelProbeButtonProps) {
  const { t } = useTranslation();
  const [pending, setPending] = useState(false);
  const [result, setResult] = useState<ModelProbeResult>();
  const running = useRef(false);
  const sequence = useRef(0);
  const controller = useRef<AbortController>();
  const reportPending = useRef(props.onPendingChange);
  reportPending.current = props.onPendingChange;
  const requestKey = JSON.stringify([props.providerId, props.groupId, props.modelId, props.modelName, props.configurationKey]);

  useEffect(() => {
    setResult(undefined);
    setPending(false);
    return () => {
      sequence.current += 1;
      controller.current?.abort();
      if (running.current) reportPending.current?.(false);
      running.current = false;
    };
  }, [requestKey]);

  const run = async () => {
    if (running.current || props.disabled || props.disabledReason) return;
    const request = ++sequence.current;
    const abort = new AbortController();
    const started = Date.now();
    controller.current = abort;
    running.current = true;
    setPending(true);
    setResult(undefined);
    reportPending.current?.(true);
    try {
      const next = await probeModel({ providerId: props.providerId, groupId: props.groupId, modelId: props.modelId }, abort.signal);
      if (request === sequence.current && !abort.signal.aborted) setResult(next);
    } catch (error) {
      if (request === sequence.current && !abort.signal.aborted) setResult({ status: "failed", code: probeFailureCode(error), elapsed_ms: Math.max(0, Date.now() - started), model_type: "" });
    } finally {
      if (request === sequence.current) {
        running.current = false;
        controller.current = undefined;
        setPending(false);
        reportPending.current?.(false);
      }
    }
  };
  const status = result?.status;
  const latency = result?.status === "passed" ? result.elapsed_ms >= 3000 ? "slow" : "normal" : undefined;
  const Icon = status === "passed" ? CheckCircleOutlined : status === "failed" ? CloseCircleOutlined : status === "accepted" ? ClockCircleOutlined : status === "unsupported" ? MinusCircleOutlined : ApiOutlined;
  const name = t(`modelProvider.probe.${pending ? "running" : status || "action"}`, { name: props.modelName });
  const title = props.disabledReason || (pending ? t("modelProvider.probe.running", { name: props.modelName }) : result ? (
    <div className="model-probe-result">
      <strong>{t(`modelProvider.probe.${result.status}`, { name: props.modelName })}</strong>
      <span>{t(`modelProvider.probe.reason.${result.code}`)}</span>
      <small>{t("modelProvider.probe.elapsed", { elapsed: result.elapsed_ms })}</small>
    </div>
  ) : t("modelProvider.probe.hint"));
  return <Tooltip title={title} trigger={["hover", "focus"]}>
    <span className="model-probe-control" tabIndex={props.disabledReason ? 0 : undefined} aria-label={props.disabledReason ? `${name} · ${props.disabledReason}` : undefined}>
      <Button type="text" aria-label={name} aria-busy={pending} data-probe-status={status} data-probe-latency={latency}
        icon={<Icon aria-hidden="true" />} loading={pending} disabled={pending || props.disabled || Boolean(props.disabledReason)} onClick={() => void run()} />
      <span className="model-probe-announcement" role="status" aria-live="polite">{status ? name : ""}</span>
    </span>
  </Tooltip>;
}
