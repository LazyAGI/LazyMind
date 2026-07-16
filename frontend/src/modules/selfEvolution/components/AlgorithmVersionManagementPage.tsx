import { useCallback, useEffect, useMemo, useState, type ChangeEvent, type MouseEvent } from "react";
import {
  Button,
  Descriptions,
  Drawer,
  Dropdown,
  Empty,
  Form,
  Input,
  InputNumber,
  Modal,
  Segmented,
  Select,
  Space,
  Spin,
  Table,
  Tag,
  Tooltip,
  Typography,
  message,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import type { InputNumberProps } from "antd";
import type { MenuProps } from "antd";
import {
  AppstoreOutlined,
  ArrowLeftOutlined,
  BarsOutlined,
  CopyOutlined,
  DeleteOutlined,
  MoreOutlined,
  PauseCircleOutlined,
  PlayCircleOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  ThunderboltOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  deleteRouterAlgorithm,
  fetchRouterAlgorithms,
  getRouterApiErrorMessage,
  registerRouterAlgorithm,
  runRouterAlgorithmAction,
  type RegisterRouterAlgorithmPayload,
  type RouterAlgorithm,
  type RouterAlgorithmAction,
} from "../shared/routerApi";
import "../index.scss";

const { Paragraph, Text, Title } = Typography;

type AlgorithmFilters = {
  threadId: string;
  algorithmId: string;
  status: string;
};

type RegisterFormValues = {
  algorithm_id: string;
  code_path: string;
  name?: string;
  thread_id: string;
  candidate_ref?: string;
  instance_count?: number;
  wait_ready_seconds?: number;
  cleanup_policy?: "thread_delete" | "manual";
};

function statusColor(status: string) {
  const normalized = status.toLowerCase();
  if (normalized === "active") {
    return "success";
  }
  if (normalized === "starting") {
    return "processing";
  }
  if (normalized === "disabled") {
    return "warning";
  }
  if (normalized === "missing") {
    return "default";
  }
  return "default";
}

export function AlgorithmVersionManagementPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [registerForm] = Form.useForm<RegisterFormValues>();

  const [draftFilters, setDraftFilters] = useState<AlgorithmFilters>({
    threadId: "",
    algorithmId: "",
    status: "all",
  });
  const [filters, setFilters] = useState<AlgorithmFilters>(draftFilters);
  const [algorithms, setAlgorithms] = useState<RouterAlgorithm[]>([]);
  const [loading, setLoading] = useState(false);
  const [actionLoadingId, setActionLoadingId] = useState("");
  const [detail, setDetail] = useState<RouterAlgorithm | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [viewMode, setViewMode] = useState<"table" | "card">("card");
  const [registerOpen, setRegisterOpen] = useState(false);
  const [registerSubmitting, setRegisterSubmitting] = useState(false);

  const loadAll = useCallback(async () => {
    setLoading(true);
    try {
      const items = await fetchRouterAlgorithms({
        threadId: filters.threadId,
        algorithmId: filters.algorithmId,
        status: filters.status,
      });
      setAlgorithms(items);
    } catch (error) {
      message.error(
        getRouterApiErrorMessage(error, t("selfEvolutionRun.algorithmManagementLoadFailed")),
      );
      setAlgorithms([]);
    } finally {
      setLoading(false);
    }
  }, [filters, t]);

  useEffect(() => {
    void loadAll();
  }, [loadAll]);

  const applyFilters = useCallback(() => {
    setFilters({
      threadId: draftFilters.threadId.trim(),
      algorithmId: draftFilters.algorithmId.trim(),
      status: draftFilters.status,
    });
  }, [draftFilters]);

  const resetFilters = useCallback(() => {
    const empty = { threadId: "", algorithmId: "", status: "all" };
    setDraftFilters(empty);
    setFilters(empty);
  }, []);

  const openDetail = useCallback((record: RouterAlgorithm) => {
    setDetail(record);
    setDrawerOpen(true);
  }, []);

  const runAction = useCallback(async (record: RouterAlgorithm, action: RouterAlgorithmAction) => {
    setActionLoadingId(`${record.algorithm_id}:${action}`);
    try {
      await runRouterAlgorithmAction(record.algorithm_id, action);
      message.success(
        t("selfEvolutionRun.algorithmManagementActionSuccess", {
          action,
          id: record.algorithm_id,
        }),
      );
      await loadAll();
    } catch (error) {
      message.error(
        getRouterApiErrorMessage(error, t("selfEvolutionRun.algorithmManagementActionFailed")),
      );
    } finally {
      setActionLoadingId("");
    }
  }, [loadAll, t]);

  const confirmDelete = useCallback((record: RouterAlgorithm) => {
    Modal.confirm({
      title: t("selfEvolutionRun.algorithmManagementDeleteTitle"),
      content: t("selfEvolutionRun.algorithmManagementDeleteContent", {
        id: record.algorithm_id,
      }),
      okText: t("common.delete"),
      okButtonProps: { danger: true },
      cancelText: t("common.cancel"),
      onOk: async () => {
        setActionLoadingId(`${record.algorithm_id}:delete`);
        try {
          await deleteRouterAlgorithm(record.algorithm_id);
          message.success(t("selfEvolutionRun.algorithmManagementDeleteSuccess"));
          if (detail?.algorithm_id === record.algorithm_id) {
            setDrawerOpen(false);
            setDetail(null);
          }
          await loadAll();
        } catch (error) {
          message.error(
            getRouterApiErrorMessage(error, t("selfEvolutionRun.algorithmManagementDeleteFailed")),
          );
          throw error;
        } finally {
          setActionLoadingId("");
        }
      },
    });
  }, [detail?.algorithm_id, loadAll, t]);

  const submitRegister = useCallback(async () => {
    try {
      const values = await registerForm.validateFields();
      const payload: RegisterRouterAlgorithmPayload = {
        algorithm_id: values.algorithm_id.trim(),
        code_path: values.code_path.trim(),
        owner: {
          thread_id: values.thread_id.trim(),
          candidate_ref: values.candidate_ref?.trim() || undefined,
        },
        name: values.name?.trim() || undefined,
        instance_count: values.instance_count,
        wait_ready_seconds: values.wait_ready_seconds,
        cleanup_policy: values.cleanup_policy,
      };
      setRegisterSubmitting(true);
      await registerRouterAlgorithm(payload);
      message.success(t("selfEvolutionRun.algorithmManagementRegisterSuccess"));
      setRegisterOpen(false);
      registerForm.resetFields();
      await loadAll();
    } catch (error) {
      if ((error as { errorFields?: unknown })?.errorFields) {
        return;
      }
      message.error(
        getRouterApiErrorMessage(error, t("selfEvolutionRun.algorithmManagementRegisterFailed")),
      );
    } finally {
      setRegisterSubmitting(false);
    }
  }, [loadAll, registerForm, t]);

  const columns = useMemo<ColumnsType<RouterAlgorithm>>(() => [
    {
      title: t("selfEvolutionRun.algorithmManagementAlgorithmId"),
      dataIndex: "algorithm_id",
      ellipsis: true,
      render: (value: string) => (
        <div className="self-evolution-algorithm-id-cell">
          <span className="self-evolution-algorithm-mono" title={value}>{value}</span>
          <Typography.Text
            copyable={{ text: value, tooltips: false, icon: <CopyOutlined /> }}
            className="self-evolution-algorithm-copy-btn"
          />
        </div>
      ),
    },
    {
      title: t("selfEvolutionRun.algorithmManagementStatus"),
      dataIndex: "status",
      width: 110,
      render: (value: string) => (
        <div className="self-evolution-algorithm-status-cell">
          <span className={`self-evolution-algorithm-status-dot status-${statusColor(value)}`} />
          <span className="self-evolution-algorithm-status-text">{value || "-"}</span>
        </div>
      ),
    },
    {
      title: t("selfEvolutionRun.algorithmManagementThreadId"),
      width: 160,
      ellipsis: true,
      render: (_, record) => (
        <Text className="self-evolution-algorithm-mono" ellipsis={{ tooltip: record.owner.thread_id }}>
          {record.owner.thread_id || "-"}
        </Text>
      ),
    },
    {
      title: t("common.actions"),
      width: 90,
      fixed: "right",
      render: (_, record) => {
        const busy = (action: string) => actionLoadingId === `${record.algorithm_id}:${action}`;
        return (
          <div className="self-evolution-algorithm-actions-cell" onClick={(e: MouseEvent) => e.stopPropagation()}>
            <Tooltip title={t("selfEvolutionRun.algorithmManagementActionHealthcheck")}>
              <Button
                type="text"
                className="action-btn action-btn-health"
                icon={<SafetyCertificateOutlined />}
                loading={busy("healthcheck")}
                onClick={() => void runAction(record, "healthcheck")}
              />
            </Tooltip>
            <div className="action-divider" />
            <Tooltip title={t("common.delete")}>
              <Button
                type="text"
                className="action-btn action-btn-delete"
                icon={<DeleteOutlined />}
                loading={busy("delete")}
                onClick={() => confirmDelete(record)}
              />
            </Tooltip>
          </div>
        );
      },
    },
  ], [actionLoadingId, confirmDelete, runAction, t]);

  return (
    <div className="self-evolution-algorithm-page">
      <div className="self-evolution-algorithm-shell">
        <header className="self-evolution-algorithm-header">
          <div className="self-evolution-algorithm-title-group">
            <Button
              type="text"
              className="self-evolution-algorithm-back"
              icon={<ArrowLeftOutlined />}
              onClick={() => navigate("/self-evolution")}
            >
              {t("common.back")}
            </Button>
            <div className="self-evolution-algorithm-title-copy">
              <Title level={4}>{t("selfEvolutionRun.algorithmManagement")}</Title>
              <Text type="secondary" className="self-evolution-algorithm-subtitle">
                {t("selfEvolutionRun.algorithmManagementSubtitle")}
              </Text>
            </div>
          </div>
        </header>

        <section className="self-evolution-algorithm-panel">
          <div className="self-evolution-algorithm-toolbar">
            <div className="self-evolution-algorithm-filterbar">
              <Input
                allowClear
                className="self-evolution-algorithm-filter-input"
                placeholder={t("selfEvolutionRun.algorithmManagementThreadFilter")}
                value={draftFilters.threadId}
                onChange={(event: ChangeEvent<HTMLInputElement>) => setDraftFilters((prev) => ({ ...prev, threadId: event.target.value }))}
                onPressEnter={applyFilters}
              />
              <Input
                allowClear
                className="self-evolution-algorithm-filter-input"
                placeholder={t("selfEvolutionRun.algorithmManagementAlgorithmFilter")}
                value={draftFilters.algorithmId}
                onChange={(event: ChangeEvent<HTMLInputElement>) => setDraftFilters((prev) => ({ ...prev, algorithmId: event.target.value }))}
                onPressEnter={applyFilters}
              />
              <Select
                className="self-evolution-algorithm-filter-select"
                value={draftFilters.status}
                options={[
                  { value: "all", label: "all" },
                  { value: "starting", label: "starting" },
                  { value: "active", label: "active" },
                  { value: "disabled", label: "disabled" },
                  { value: "missing", label: "missing" },
                ]}
                onChange={(value: string) => setDraftFilters((prev) => ({ ...prev, status: value }))}
              />
              <Button type="primary" onClick={applyFilters}>{t("common.search")}</Button>
              <Button onClick={resetFilters}>{t("common.reset")}</Button>
              <Button
                icon={<ThunderboltOutlined />}
                onClick={() => navigate("/self-evolution/algorithms/routing-strategy")}
              >
                {t("selfEvolutionRun.algorithmManagementAbTitle")}
              </Button>
            </div>
            <Space size={16} align="center" className="self-evolution-algorithm-toolbar-right">
              <Text type="secondary" className="self-evolution-algorithm-count">
                {t("selfEvolutionRun.algorithmManagementPageInfo", { count: algorithms.length })}
              </Text>
              <Segmented
                value={viewMode}
                onChange={(val) => setViewMode(val as "table" | "card")}
                options={[
                  { value: "table", icon: <BarsOutlined /> },
                  { value: "card", icon: <AppstoreOutlined /> },
                ]}
              />
            </Space>
          </div>

          {viewMode === "table" ? (
            <Table<RouterAlgorithm>
              className="self-evolution-algorithm-table"
              columns={columns}
              dataSource={algorithms}
              rowKey="algorithm_id"
              loading={loading}
              pagination={false}
              size="middle"
              tableLayout="fixed"
              scroll={{ x: 900 }}
              locale={{ emptyText: <Empty description={t("selfEvolutionRun.algorithmManagementNoAlgorithms")} /> }}
              onRow={(record: RouterAlgorithm) => ({ onClick: () => openDetail(record) })}
            />
          ) : (
            <div className="self-evolution-algorithm-card-view">
              {loading ? (
                <div className="self-evolution-algorithm-loading"><Spin /></div>
              ) : algorithms.length === 0 ? (
                <Empty description={t("selfEvolutionRun.algorithmManagementNoAlgorithms")} style={{ padding: "40px 0" }} />
              ) : (
                <div className="self-evolution-algorithm-card-grid">
                  {algorithms.map((record) => {
                    const busy = (action: string) => actionLoadingId === `${record.algorithm_id}:${action}`;
                    const isPaused = record.status.toLowerCase() === "disabled";
                    const serviceAction: RouterAlgorithmAction = isPaused ? "start" : "stop";

                    return (
                      <div
                        key={record.algorithm_id}
                        className="self-evolution-algorithm-card"
                        onClick={() => openDetail(record)}
                      >
                        <div className="card-header">
                          <div className="card-title">
                            <span className="self-evolution-algorithm-mono" title={record.algorithm_id}>
                              {record.algorithm_id}
                            </span>
                            <Typography.Text
                              copyable={{ text: record.algorithm_id, tooltips: false, icon: <CopyOutlined /> }}
                              className="self-evolution-algorithm-copy-btn"
                              onClick={(e) => e.stopPropagation()}
                            />
                          </div>
                          <div className="self-evolution-algorithm-status-cell">
                            <span className={`self-evolution-algorithm-status-dot status-${statusColor(record.status)}`} />
                            <span className="self-evolution-algorithm-status-text">{record.status || "-"}</span>
                          </div>
                        </div>
                        <div className="card-body">
                          <div className="card-field">
                            <span className="field-label">{t("selfEvolutionRun.algorithmManagementThreadId")}</span>
                            <Text className="self-evolution-algorithm-mono" ellipsis={{ tooltip: record.owner.thread_id }}>
                              {record.owner.thread_id || "-"}
                            </Text>
                          </div>
                        </div>
                        <div className="card-footer" onClick={(e: MouseEvent) => e.stopPropagation()}>
                          <div className="self-evolution-algorithm-actions-cell">
                            <Tooltip title={t("selfEvolutionRun.algorithmManagementActionHealthcheck")}>
                              <Button
                                type="text"
                                className="action-btn action-btn-health"
                                icon={<SafetyCertificateOutlined />}
                                loading={busy("healthcheck")}
                                onClick={() => void runAction(record, "healthcheck")}
                              />
                            </Tooltip>
                            <Tooltip title={t("selfEvolutionRun.algorithmManagementActionRestart")}>
                              <Button
                                type="text"
                                className="action-btn action-btn-restart"
                                icon={<ReloadOutlined />}
                                loading={busy("restart")}
                                onClick={() => void runAction(record, "restart")}
                              />
                            </Tooltip>
                            <Tooltip
                              title={t(isPaused
                                ? "selfEvolutionRun.algorithmManagementActionStart"
                                : "selfEvolutionRun.algorithmManagementActionStop")}
                            >
                              <Button
                                type="text"
                                className={`action-btn action-btn-${serviceAction}`}
                                icon={isPaused ? <PlayCircleOutlined /> : <PauseCircleOutlined />}
                                loading={busy(serviceAction)}
                                onClick={() => void runAction(record, serviceAction)}
                              />
                            </Tooltip>
                            <div className="action-divider" />
                            <Tooltip title={t("common.delete")}>
                              <Button
                                type="text"
                                className="action-btn action-btn-delete"
                                icon={<DeleteOutlined />}
                                loading={busy("delete")}
                                onClick={() => confirmDelete(record)}
                              />
                            </Tooltip>
                          </div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}
            </div>
          )}
        </section>
      </div>

      <Drawer
        open={drawerOpen}
        width={720}
        onClose={() => setDrawerOpen(false)}
        title={t("selfEvolutionRun.algorithmManagementDetail")}
      >
        {detail ? (
          <div className="self-evolution-algorithm-detail">
            <Descriptions bordered size="small" column={1}>
              <Descriptions.Item label={t("selfEvolutionRun.algorithmManagementAlgorithmId")}>
                <Paragraph className="self-evolution-algorithm-mono" copyable={{ text: detail.algorithm_id }}>
                  {detail.algorithm_id}
                </Paragraph>
              </Descriptions.Item>
              <Descriptions.Item label={t("selfEvolutionRun.algorithmManagementStatus")}>
                <Tag color={statusColor(detail.status)}>{detail.status || "-"}</Tag>
              </Descriptions.Item>
              <Descriptions.Item label={t("selfEvolutionRun.algorithmManagementExpectedState")}>
                {detail.expected_state || "-"}
              </Descriptions.Item>
              <Descriptions.Item label={t("selfEvolutionRun.algorithmManagementThreadId")}>
                {detail.owner.thread_id || "-"}
              </Descriptions.Item>
              <Descriptions.Item label={t("selfEvolutionRun.algorithmManagementCandidateRef")}>
                {detail.owner.candidate_ref || "-"}
              </Descriptions.Item>
              <Descriptions.Item label={t("selfEvolutionRun.algorithmManagementRouterAdminUrl")}>
                {detail.router_admin_url || "-"}
              </Descriptions.Item>
              <Descriptions.Item label={t("selfEvolutionRun.algorithmManagementRouterChatUrl")}>
                {detail.router_chat_url || "-"}
              </Descriptions.Item>
            </Descriptions>
          </div>
        ) : (
          <div className="self-evolution-algorithm-loading"><Spin /></div>
        )}
      </Drawer>

      <Modal
        open={registerOpen}
        title={t("selfEvolutionRun.algorithmManagementRegister")}
        onCancel={() => setRegisterOpen(false)}
        onOk={() => void submitRegister()}
        confirmLoading={registerSubmitting}
        destroyOnClose
        width={640}
      >
        <Form
          form={registerForm}
          layout="vertical"
          initialValues={{
            instance_count: 1,
            wait_ready_seconds: 180,
            cleanup_policy: "thread_delete",
          }}
        >
          <Form.Item
            name="algorithm_id"
            label={t("selfEvolutionRun.algorithmManagementAlgorithmId")}
            rules={[
              { required: true, message: t("selfEvolutionRun.algorithmManagementFieldRequired") },
              {
                pattern: /^evo_/,
                message: t("selfEvolutionRun.algorithmManagementAlgorithmIdPrefix"),
              },
            ]}
          >
            <Input placeholder="evo_..." />
          </Form.Item>
          <Form.Item
            name="code_path"
            label={t("selfEvolutionRun.algorithmManagementCodePath")}
            rules={[{ required: true, message: t("selfEvolutionRun.algorithmManagementFieldRequired") }]}
          >
            <Input />
          </Form.Item>
          <Form.Item name="name" label={t("selfEvolutionRun.algorithmManagementName")}>
            <Input />
          </Form.Item>
          <Form.Item
            name="thread_id"
            label={t("selfEvolutionRun.algorithmManagementThreadId")}
            rules={[{ required: true, message: t("selfEvolutionRun.algorithmManagementFieldRequired") }]}
          >
            <Input />
          </Form.Item>
          <Form.Item name="candidate_ref" label={t("selfEvolutionRun.algorithmManagementCandidateRef")}>
            <Input />
          </Form.Item>
          <Space size={16} style={{ display: "flex" }} align="start">
            <Form.Item
              name="instance_count"
              label={t("selfEvolutionRun.algorithmManagementInstanceCount")}
              style={{ flex: 1 }}
            >
              <InputNumber min={1} max={4} style={{ width: "100%" }} />
            </Form.Item>
            <Form.Item
              name="wait_ready_seconds"
              label={t("selfEvolutionRun.algorithmManagementWaitReady")}
              style={{ flex: 1 }}
            >
              <InputNumber min={1} max={900} style={{ width: "100%" }} />
            </Form.Item>
          </Space>
          <Form.Item name="cleanup_policy" label={t("selfEvolutionRun.algorithmManagementCleanupPolicy")}>
            <Select
              options={[
                { value: "thread_delete", label: "thread_delete" },
                { value: "manual", label: "manual" },
              ]}
            />
          </Form.Item>
        </Form>
      </Modal>

    </div>
  );
}

export default AlgorithmVersionManagementPage;
