import { useCallback, useEffect, useState } from "react";
import { Alert, Button, Card, Collapse, Empty, Modal, Select, Spin, Tag, Typography } from "antd";
import { HistoryOutlined, ReloadOutlined, SafetyCertificateOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import {
  loadExternalCapabilityInvocations,
  type ExternalCapabilityInvocation,
  type ExternalCapabilityInvocationPage,
} from "./externalCapabilitiesApi";

const AGENT_OPTIONS = [
  { value: "codex", label: "Codex" },
  { value: "cursor", label: "Cursor" },
  { value: "workbuddy", label: "WorkBuddy" },
  { value: "raccoon", label: "Raccoon" },
  { value: "traework", label: "TRAE Work" },
  { value: "deepseek-harness", label: "DeepSeek Harness" },
];

export default function ExternalCapabilityAccess() {
  const { t } = useTranslation();
  const [agent, setAgent] = useState("codex");
  const [expanded, setExpanded] = useState(false);
  const [history, setHistory] = useState<ExternalCapabilityInvocationPage | null>(null);
  const [historyLoading, setHistoryLoading] = useState(true);
  const [historyFailed, setHistoryFailed] = useState(false);

  const refreshHistory = useCallback(async () => {
    setHistoryLoading(true);
    setHistoryFailed(false);
    try {
      setHistory(await loadExternalCapabilityInvocations(agent));
    } catch {
      setHistoryFailed(true);
    } finally {
      setHistoryLoading(false);
    }
  }, [agent]);

  useEffect(() => { if (expanded) void refreshHistory(); }, [expanded, refreshHistory]);

  return (
    <Collapse
      className="external-capability-access-card"
      activeKey={expanded ? ["history"] : []}
      onChange={(keys) => setExpanded(keys.includes("history"))}
      items={[{
        key: "history",
        label: t("agentIntegration.capabilityHistoryTitle"),
        children: <Card bordered={false}>
      <div className="external-capability-access-header">
        <div>
          <Typography.Title level={4}>
            <SafetyCertificateOutlined /> {t("agentIntegration.capabilityAccessTitle")}
          </Typography.Title>
          <Typography.Paragraph type="secondary">
            {t("agentIntegration.capabilityAccessDescription")}
          </Typography.Paragraph>
        </div>
        <Select
          aria-label={t("agentIntegration.capabilityAgentLabel")}
          value={agent}
          options={AGENT_OPTIONS}
          onChange={setAgent}
        />
      </div>
      <Alert
        type="info"
        showIcon
        message={t("agentIntegration.capabilitySecurityNotice")}
      />
      <InvocationHistoryPanel
        history={history}
        loading={historyLoading}
        failed={historyFailed}
        onRefresh={refreshHistory}
      />
        </Card>,
      }]}
    />
  );
}

function InvocationHistoryPanel({
  history, loading, failed, onRefresh,
}: {
  history: ExternalCapabilityInvocationPage | null;
  loading: boolean;
  failed: boolean;
  onRefresh: () => Promise<void>;
}) {
  const { t } = useTranslation();
  const summary = history?.summary;
  const invocations = history?.invocations || [];
  const capabilityNames = new Map(
    (summary?.capabilities || []).map((item) => [`${item.capability_type}:${item.capability_id}`, item.capability_name]),
  );
  return (
    <section className="external-capability-history">
      <div className="external-capability-history-header">
        <div>
          <Typography.Title level={5}>
            <HistoryOutlined /> {t("agentIntegration.capabilityHistoryTitle")}
          </Typography.Title>
          <Typography.Text type="secondary">
            {t("agentIntegration.capabilityHistoryDescription")}
          </Typography.Text>
        </div>
        <Button
          aria-label={t("agentIntegration.capabilityHistoryRefresh")}
          icon={<ReloadOutlined />}
          loading={loading}
          onClick={() => void onRefresh()}
        >
          {t("common.refresh")}
        </Button>
      </div>
      {failed ? (
        <Alert type="error" showIcon message={t("agentIntegration.capabilityHistoryLoadFailed")} />
      ) : (
        <Spin spinning={loading}>
          <div className="external-capability-history-stats">
            <HistoryStat label={t("agentIntegration.capabilityHistoryTotal")} value={summary?.total || 0} />
            <HistoryStat label={t("agentIntegration.capabilityHistoryModels")} value={summary?.model_calls || 0} />
            <HistoryStat label={t("agentIntegration.capabilityHistoryTools")} value={summary?.tool_calls || 0} />
            <HistoryStat label={t("agentIntegration.capabilityHistoryFailed")} value={summary?.failed || 0} danger />
          </div>
          {!!summary?.capabilities?.length && (
            <div className="external-capability-history-aggregates">
              {summary.capabilities.map((item) => (
                <div key={`${item.agent}:${item.capability_type}:${item.capability_id}`}>
                  <span>
                    <Tag color={item.capability_type === "model" ? "blue" : "purple"}>
                      {t(item.capability_type === "model"
                        ? "agentIntegration.capabilityHistoryModel"
                        : "agentIntegration.capabilityHistoryTool")}
                    </Tag>
                    {item.capability_name}
                  </span>
                  <strong>{t("agentIntegration.capabilityHistoryCalls", { count: item.call_count })}</strong>
                </div>
              ))}
            </div>
          )}
          {!loading && !invocations.length ? (
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("agentIntegration.capabilityHistoryEmpty")} />
          ) : (
            <div className="external-capability-history-table-wrap">
              <table className="external-capability-history-table">
                <thead>
                  <tr>
                    <th>{t("agentIntegration.capabilityHistoryCaller")}</th>
                    <th>{t("agentIntegration.capabilityHistoryCapability")}</th>
                    <th>{t("agentIntegration.capabilityHistoryStatus")}</th>
                    <th>{t("agentIntegration.capabilityHistoryUsage")}</th>
                    <th>{t("agentIntegration.capabilityHistoryResult")}</th>
                    <th>{t("agentIntegration.capabilityHistoryTime")}</th>
                  </tr>
                </thead>
                <tbody>
                  {invocations.map((item) => (
                    <InvocationRow
                      key={item.id}
                      invocation={item}
                      capabilityName={capabilityNames.get(`${item.capability_type}:${item.capability_id}`)}
                    />
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Spin>
      )}
    </section>
  );
}

function HistoryStat({ label, value, danger = false }: { label: string; value: number; danger?: boolean }) {
  return (
    <div className={danger && value ? "is-danger" : ""}>
      <strong>{value}</strong>
      <span>{label}</span>
    </div>
  );
}

function InvocationRow({
  invocation, capabilityName,
}: {
  invocation: ExternalCapabilityInvocation;
  capabilityName?: string;
}) {
  const { t } = useTranslation();
  const [detailsOpen, setDetailsOpen] = useState(false);
  const statusColors: Record<ExternalCapabilityInvocation["status"], string> = {
    succeeded: "success", failed: "error", running: "processing",
  };
  const hasResult = invocation.result?.data !== undefined || !!invocation.result?.preview;
  const hasDetails = hasResult || !!invocation.error_message;
  const imageURL = localResultImageURL(invocation.result?.data);
  return (
    <>
      <tr>
        <td>{agentLabel(invocation.agent)}</td>
        <td>
          <span className="external-capability-history-capability">
            {capabilityName || invocation.capability_name}
          </span>
          <small>{t(invocation.capability_type === "model"
            ? "agentIntegration.capabilityHistoryModel"
            : "agentIntegration.capabilityHistoryTool")}</small>
        </td>
        <td>
          <Tag color={statusColors[invocation.status]}>
            {t(`agentIntegration.capabilityHistoryStatus_${invocation.status}`)}
          </Tag>
        </td>
        <td>{formatUsage(invocation)}</td>
        <td>
          {hasDetails ? (
            <Button type="link" size="small" onClick={() => setDetailsOpen(true)}>
              {t("agentIntegration.capabilityHistoryViewResult")}
            </Button>
          ) : "—"}
        </td>
        <td>{new Date(invocation.started_at).toLocaleString()}</td>
      </tr>
      <Modal
        className="external-capability-result-modal"
        footer={null}
        open={detailsOpen}
        title={t("agentIntegration.capabilityHistoryResultTitle", {
          name: capabilityName || invocation.capability_name,
        })}
        onCancel={() => setDetailsOpen(false)}
        width={720}
      >
        {invocation.error_message && (
          <Alert
            className="external-capability-result-error"
            type="error"
            showIcon
            message={t("agentIntegration.capabilityHistoryFailureReason")}
            description={invocation.error_message}
          />
        )}
        {invocation.result?.truncated && (
          <Alert
            className="external-capability-result-truncated"
            type="info"
            showIcon
            message={t("agentIntegration.capabilityHistoryResultTruncated")}
          />
        )}
        {imageURL && <img className="external-capability-result-image" src={imageURL} alt="" />}
        {hasResult && (
          <pre className="external-capability-result-preview">
            {formatInvocationResult(invocation)}
          </pre>
        )}
      </Modal>
    </>
  );
}

function agentLabel(agent: string): string {
  return AGENT_OPTIONS.find((item) => item.value === agent)?.label || agent;
}

function formatUsage(invocation: ExternalCapabilityInvocation): string {
  if (typeof invocation.usage?.total_tokens === "number") {
    return `${invocation.usage.total_tokens} tokens`;
  }
  if (typeof invocation.usage?.result_bytes === "number") {
    return `${invocation.usage.result_bytes} B`;
  }
  return "—";
}

function formatInvocationResult(invocation: ExternalCapabilityInvocation): string {
  if (invocation.result?.preview) return invocation.result.preview;
  if (typeof invocation.result?.data === "string") return invocation.result.data;
  return JSON.stringify(invocation.result?.data, null, 2);
}

function localResultImageURL(value: unknown): string | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  const imageURL = (value as Record<string, unknown>).image_url;
  if (typeof imageURL !== "string") return undefined;
  if (imageURL.startsWith("/static-files/")) return `/api/core${imageURL}`;
  return imageURL.startsWith("/api/core/static-files/")
    ? imageURL
    : undefined;
}
