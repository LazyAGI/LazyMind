import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Button,
  Descriptions,
  Drawer,
  Empty,
  Input,
  Select,
  Space,
  Spin,
  Table,
  Tag,
  Typography,
  message,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  ArrowLeftOutlined,
  EyeOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { axiosInstance, getLocalizedErrorMessage } from "@/components/request";
import { AGENT_API_BASE } from "../shared/constants";
import "../index.scss";

const { Paragraph, Text, Title } = Typography;

type CandidateSummary = {
  status?: string;
  verdict?: string;
  reasons?: unknown;
  delta?: unknown;
  algo_id?: string;
  candidate_algo_id?: string;
  diff_files?: string[];
  [key: string]: unknown;
};

type CandidateItem = {
  candidate_id: string;
  thread_id: string;
  source_step: string;
  source_ref: string;
  status: string;
  summary: CandidateSummary;
  files?: string[];
};

type CandidateFilters = {
  threadId: string;
  status: string;
  sourceStep: string;
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function asString(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function asStringArray(value: unknown) {
  if (Array.isArray(value)) {
    return value.map((item) => asString(item)).filter(Boolean);
  }
  const text = asString(value);
  return text ? [text] : [];
}

function normalizeCandidateItem(value: unknown): CandidateItem | undefined {
  if (!isRecord(value)) {
    return undefined;
  }
  const rawSummary = isRecord(value.summary) ? value.summary : {};
  const summary: CandidateSummary = {
    ...rawSummary,
    diff_files: asStringArray(rawSummary.diff_files),
  };
  const candidateId = asString(value.candidate_id);
  if (!candidateId) {
    return undefined;
  }
  return {
    candidate_id: candidateId,
    thread_id: asString(value.thread_id),
    source_step: asString(value.source_step),
    source_ref: asString(value.source_ref),
    status: asString(value.status),
    summary,
    files: asStringArray(value.files),
  };
}

function normalizeCandidateList(value: unknown) {
  if (!isRecord(value)) {
    return { items: [], next_page_token: "" };
  }
  return {
    items: (Array.isArray(value.items) ? value.items : [])
      .map(normalizeCandidateItem)
      .filter((item): item is CandidateItem => Boolean(item)),
    next_page_token: asString(value.next_page_token),
  };
}

function prettyJson(value: unknown) {
  try {
    return JSON.stringify(value ?? {}, null, 2);
  } catch {
    return String(value ?? "");
  }
}

function statusColor(status: string) {
  const normalized = status.toLowerCase();
  if (["active", "ready", "passed", "done", "success"].includes(normalized)) {
    return "green";
  }
  if (["testing", "running", "pending", "draft"].includes(normalized)) {
    return "blue";
  }
  if (["deprecated", "stopped", "missing"].includes(normalized)) {
    return "orange";
  }
  if (["failed", "error", "rejected"].includes(normalized)) {
    return "red";
  }
  return "default";
}

function sourceStepColor(sourceStep: string) {
  return sourceStep === "abtest" ? "purple" : "cyan";
}

function candidateAlgorithmId(candidate: CandidateItem) {
  return asString(candidate.summary.candidate_algo_id) || asString(candidate.summary.algo_id);
}

export function AlgorithmVersionManagementPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();

  const [candidateDraftFilters, setCandidateDraftFilters] = useState<CandidateFilters>({
    threadId: "",
    status: "",
    sourceStep: "",
  });
  const [candidateFilters, setCandidateFilters] = useState<CandidateFilters>(candidateDraftFilters);
  const [candidatePageSize, setCandidatePageSize] = useState(20);
  const [candidatePageToken, setCandidatePageToken] = useState("");
  const [candidatePageStack, setCandidatePageStack] = useState<string[]>([]);
  const [candidateNextToken, setCandidateNextToken] = useState("");
  const [candidates, setCandidates] = useState<CandidateItem[]>([]);
  const [candidateLoading, setCandidateLoading] = useState(false);
  const [candidateDetail, setCandidateDetail] = useState<CandidateItem | null>(null);
  const [candidateDetailLoading, setCandidateDetailLoading] = useState(false);
  const [candidateDrawerOpen, setCandidateDrawerOpen] = useState(false);

  const loadCandidates = useCallback(async (pageToken: string) => {
    setCandidateLoading(true);
    try {
      const params: Record<string, string | number> = {
        page_size: candidatePageSize,
      };
      if (candidateFilters.threadId) {
        params.thread_id = candidateFilters.threadId;
      }
      if (candidateFilters.status) {
        params.status = candidateFilters.status;
      }
      if (pageToken) {
        params.page_token = pageToken;
      }
      const response = await axiosInstance.get(`${AGENT_API_BASE}/candidates`, {
        params,
        silentError: true,
      } as Parameters<typeof axiosInstance.get>[1]);
      const normalized = normalizeCandidateList(response.data);
      setCandidates(normalized.items);
      setCandidateNextToken(normalized.next_page_token);
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("selfEvolutionRun.algorithmManagementLoadCandidatesFailed")));
    } finally {
      setCandidateLoading(false);
    }
  }, [candidateFilters, candidatePageSize, t]);

  useEffect(() => {
    void loadCandidates(candidatePageToken);
  }, [candidatePageToken, loadCandidates]);

  const filteredCandidates = useMemo(() => {
    if (!candidateFilters.sourceStep) {
      return candidates;
    }
    return candidates.filter((item) => item.source_step === candidateFilters.sourceStep);
  }, [candidateFilters.sourceStep, candidates]);

  const openCandidateDetail = useCallback(async (candidate: CandidateItem) => {
    setCandidateDrawerOpen(true);
    setCandidateDetail(candidate);
    setCandidateDetailLoading(true);
    try {
      const response = await axiosInstance.get(
        `${AGENT_API_BASE}/candidates/${encodeURIComponent(candidate.candidate_id)}`,
        { silentError: true } as Parameters<typeof axiosInstance.get>[1],
      );
      setCandidateDetail(normalizeCandidateItem(response.data) ?? candidate);
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("selfEvolutionRun.algorithmManagementLoadCandidateDetailFailed")));
    } finally {
      setCandidateDetailLoading(false);
    }
  }, [t]);

  const resetCandidatePaging = useCallback(() => {
    setCandidatePageStack([]);
    setCandidatePageToken("");
  }, []);

  const applyCandidateFilters = useCallback(() => {
    setCandidateFilters({
      threadId: candidateDraftFilters.threadId.trim(),
      status: candidateDraftFilters.status.trim(),
      sourceStep: candidateDraftFilters.sourceStep,
    });
    resetCandidatePaging();
  }, [candidateDraftFilters, resetCandidatePaging]);

  const resetCandidateFilters = useCallback(() => {
    const emptyFilters = { threadId: "", status: "", sourceStep: "" };
    setCandidateDraftFilters(emptyFilters);
    setCandidateFilters(emptyFilters);
    resetCandidatePaging();
  }, [resetCandidatePaging]);

  const candidateColumns = useMemo<ColumnsType<CandidateItem>>(() => [
    {
      title: t("selfEvolutionRun.algorithmManagementCandidateId"),
      dataIndex: "candidate_id",
      width: 280,
      render: (value: string) => (
        <Paragraph className="self-evolution-algorithm-mono" copyable={{ text: value }} ellipsis={{ rows: 2 }}>
          {value}
        </Paragraph>
      ),
    },
    {
      title: t("selfEvolutionRun.algorithmManagementThreadId"),
      dataIndex: "thread_id",
      width: 160,
      render: (value: string) => <Text className="self-evolution-algorithm-mono">{value || "-"}</Text>,
    },
    {
      title: t("selfEvolutionRun.algorithmManagementSourceStep"),
      dataIndex: "source_step",
      width: 120,
      render: (value: string) => value ? <Tag color={sourceStepColor(value)}>{value}</Tag> : "-",
    },
    {
      title: t("selfEvolutionRun.algorithmManagementStatus"),
      dataIndex: "status",
      width: 120,
      render: (value: string) => value ? <Tag color={statusColor(value)}>{value}</Tag> : <Tag>{t("selfEvolutionRun.algorithmManagementEmptyStatus")}</Tag>,
    },
    {
      title: t("selfEvolutionRun.algorithmManagementVerdict"),
      width: 140,
      render: (_, record) => asString(record.summary.verdict) || "-",
    },
    {
      title: t("selfEvolutionRun.algorithmManagementCandidateAlgorithm"),
      width: 180,
      render: (_, record) => candidateAlgorithmId(record) || "-",
    },
    {
      title: t("selfEvolutionRun.algorithmManagementDiffFiles"),
      width: 110,
      render: (_, record) => record.summary.diff_files?.length ?? 0,
    },
    {
      title: t("common.actions"),
      width: 96,
      fixed: "right",
      render: (_, record) => (
        <Button
          type="link"
          size="small"
          icon={<EyeOutlined />}
          onClick={(event) => {
            event.stopPropagation();
            void openCandidateDetail(record);
          }}
        >
          {t("common.view")}
        </Button>
      ),
    },
  ], [openCandidateDetail, t]);

  return (
    <div className="self-evolution-algorithm-page">
      <div className="self-evolution-algorithm-shell">
        <header className="self-evolution-algorithm-header">
          <div className="self-evolution-algorithm-title-group">
            <Button icon={<ArrowLeftOutlined />} onClick={() => navigate("/self-evolution")}>
              {t("common.back")}
            </Button>
            <div>
              <Title level={3}>{t("selfEvolutionRun.algorithmManagement")}</Title>
              <Text type="secondary">{t("selfEvolutionRun.algorithmManagementSubtitle")}</Text>
            </div>
          </div>
          <Button icon={<ReloadOutlined />} onClick={() => void loadCandidates(candidatePageToken)}>
            {t("common.refresh")}
          </Button>
        </header>

        <section className="self-evolution-algorithm-panel">
          <div className="self-evolution-algorithm-filterbar">
            <Input
              allowClear
              className="self-evolution-algorithm-filter-input"
              placeholder={t("selfEvolutionRun.algorithmManagementThreadFilter")}
              value={candidateDraftFilters.threadId}
              onChange={(event) => setCandidateDraftFilters((prev) => ({ ...prev, threadId: event.target.value }))}
              onPressEnter={applyCandidateFilters}
            />
            <Input
              allowClear
              className="self-evolution-algorithm-filter-input"
              placeholder={t("selfEvolutionRun.algorithmManagementStatusFilter")}
              value={candidateDraftFilters.status}
              onChange={(event) => setCandidateDraftFilters((prev) => ({ ...prev, status: event.target.value }))}
              onPressEnter={applyCandidateFilters}
            />
            <Select
              allowClear
              className="self-evolution-algorithm-filter-select"
              placeholder={t("selfEvolutionRun.algorithmManagementSourceStepFilter")}
              value={candidateDraftFilters.sourceStep || undefined}
              options={[
                { value: "repair", label: "repair" },
                { value: "abtest", label: "abtest" },
              ]}
              onChange={(value) => setCandidateDraftFilters((prev) => ({ ...prev, sourceStep: value || "" }))}
            />
            <Select
              className="self-evolution-algorithm-filter-size"
              value={candidatePageSize}
              options={[20, 50, 100, 200].map((value) => ({ value, label: t("selfEvolutionRun.algorithmManagementPageSize", { size: value }) }))}
              onChange={(value) => {
                setCandidatePageSize(value);
                resetCandidatePaging();
              }}
            />
            <Space wrap className="self-evolution-algorithm-filter-actions">
              <Button type="primary" onClick={applyCandidateFilters}>{t("common.search")}</Button>
              <Button onClick={resetCandidateFilters}>{t("common.reset")}</Button>
            </Space>
          </div>
          <Table<CandidateItem>
            className="self-evolution-algorithm-table"
            columns={candidateColumns}
            dataSource={filteredCandidates}
            rowKey="candidate_id"
            loading={candidateLoading}
            pagination={false}
            tableLayout="fixed"
            scroll={{ x: 1280 }}
            locale={{ emptyText: <Empty description={t("selfEvolutionRun.algorithmManagementNoCandidates")} /> }}
            onRow={(record) => ({ onClick: () => void openCandidateDetail(record) })}
          />
          <footer className="self-evolution-algorithm-pager">
            <Text type="secondary">
              {t("selfEvolutionRun.algorithmManagementPageInfo", { count: filteredCandidates.length })}
            </Text>
            <Space>
              <Button
                disabled={candidatePageStack.length === 0 || candidateLoading}
                onClick={() => {
                  const previousStack = candidatePageStack.slice(0, -1);
                  setCandidatePageToken(candidatePageStack[candidatePageStack.length - 1] || "");
                  setCandidatePageStack(previousStack);
                }}
              >
                {t("common.previous")}
              </Button>
              <Button
                disabled={!candidateNextToken || candidateLoading}
                onClick={() => {
                  setCandidatePageStack((prev) => [...prev, candidatePageToken]);
                  setCandidatePageToken(candidateNextToken);
                }}
              >
                {t("common.next")}
              </Button>
            </Space>
          </footer>
        </section>
      </div>

      <Drawer
        open={candidateDrawerOpen}
        width={720}
        onClose={() => setCandidateDrawerOpen(false)}
        title={t("selfEvolutionRun.algorithmManagementCandidateDetail")}
      >
        {candidateDetailLoading ? (
          <div className="self-evolution-algorithm-loading"><Spin /></div>
        ) : candidateDetail ? (
          <div className="self-evolution-algorithm-detail">
            <Descriptions bordered size="small" column={1}>
              <Descriptions.Item label={t("selfEvolutionRun.algorithmManagementCandidateId")}>
                <Paragraph className="self-evolution-algorithm-mono" copyable={{ text: candidateDetail.candidate_id }}>
                  {candidateDetail.candidate_id}
                </Paragraph>
              </Descriptions.Item>
              <Descriptions.Item label={t("selfEvolutionRun.algorithmManagementThreadId")}>{candidateDetail.thread_id || "-"}</Descriptions.Item>
              <Descriptions.Item label={t("selfEvolutionRun.algorithmManagementSourceStep")}>{candidateDetail.source_step || "-"}</Descriptions.Item>
              <Descriptions.Item label={t("selfEvolutionRun.algorithmManagementSourceRef")}>{candidateDetail.source_ref || "-"}</Descriptions.Item>
              <Descriptions.Item label={t("selfEvolutionRun.algorithmManagementStatus")}>
                {candidateDetail.status ? <Tag color={statusColor(candidateDetail.status)}>{candidateDetail.status}</Tag> : "-"}
              </Descriptions.Item>
            </Descriptions>

            <section>
              <Text strong>{t("selfEvolutionRun.algorithmManagementSummary")}</Text>
              <pre className="self-evolution-algorithm-json">{prettyJson(candidateDetail.summary)}</pre>
            </section>

            <section>
              <Text strong>{t("selfEvolutionRun.algorithmManagementFiles")}</Text>
              {candidateDetail.files?.length ? (
                <div className="self-evolution-algorithm-file-list">
                  {candidateDetail.files.map((file) => <Tag key={file}>{file}</Tag>)}
                </div>
              ) : (
                <Text type="secondary">{t("selfEvolutionRun.algorithmManagementNoFiles")}</Text>
              )}
            </section>
          </div>
        ) : (
          <Empty />
        )}
      </Drawer>
    </div>
  );
}

export default AlgorithmVersionManagementPage;
