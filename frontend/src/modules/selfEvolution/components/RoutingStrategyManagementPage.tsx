import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ChangeEvent,
} from "react";
import {
  Button,
  Empty,
  Input,
  InputNumber,
  Modal,
  Skeleton,
  Slider,
  Tag,
  Typography,
  message,
} from "antd";
import {
  ArrowLeftOutlined,
  CheckOutlined,
  DeleteOutlined,
  PlusOutlined,
  ReloadOutlined,
  SearchOutlined,
  WarningOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  fetchRouterABStrategy,
  fetchRouterAlgorithms,
  getRouterApiErrorMessage,
  putRouterABStrategy,
  type RouterABStrategy,
  type RouterAlgorithm,
} from "../shared/routerApi";
import "../index.scss";

const { Text } = Typography;
const MAX_ROUTING_ALGORITHMS = 2;

type WeightRow = {
  key: string;
  algorithmId: string;
  weight: number;
};

function createWeightRows(weights: Record<string, number>): WeightRow[] {
  const entries = Object.entries(weights).slice(0, MAX_ROUTING_ALGORITHMS);
  if (!entries.length) {
    return [];
  }

  const total = entries.reduce((sum, [, weight]) => sum + weight, 0);
  if (entries.length === 1) {
    return [{ key: `row-0-${entries[0][0]}`, algorithmId: entries[0][0], weight: 100 }];
  }

  const firstWeight = total > 0 ? Math.round((entries[0][1] / total) * 100) : 50;
  return [
    { key: `row-0-${entries[0][0]}`, algorithmId: entries[0][0], weight: firstWeight },
    { key: `row-1-${entries[1][0]}`, algorithmId: entries[1][0], weight: 100 - firstWeight },
  ];
}

function requireStrategy(
  strategy: RouterABStrategy | null,
  errorMessage: string,
): RouterABStrategy {
  if (!strategy) {
    throw new Error(errorMessage);
  }
  return strategy;
}

function strategySignature(weights: Record<string, number>) {
  return JSON.stringify(Object.entries(weights).sort(([left], [right]) => left.localeCompare(right)));
}

function statusColor(status: string) {
  if (status.toLowerCase() === "active") {
    return "success";
  }
  if (status.toLowerCase() === "starting") {
    return "processing";
  }
  if (status.toLowerCase() === "disabled") {
    return "warning";
  }
  return "default";
}

export function RoutingStrategyManagementPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const pageTitleRef = useRef<HTMLHeadingElement>(null);
  const loadRequestIdRef = useRef(0);
  const isMountedRef = useRef(true);
  const [algorithms, setAlgorithms] = useState<RouterAlgorithm[]>([]);
  const [strategy, setStrategy] = useState<RouterABStrategy | null>(null);
  const [weightRows, setWeightRows] = useState<WeightRow[]>([]);
  const [reason, setReason] = useState("");
  const [searchText, setSearchText] = useState("");
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [clearing, setClearing] = useState(false);

  const loadStrategy = useCallback(async () => {
    const requestId = ++loadRequestIdRef.current;
    setLoading(true);
    setLoadError("");
    try {
      const [items, nextStrategy] = await Promise.all([
        fetchRouterAlgorithms(),
        fetchRouterABStrategy(),
      ]);
      if (requestId !== loadRequestIdRef.current) {
        return;
      }
      const resolvedStrategy = requireStrategy(
        nextStrategy,
        t("selfEvolutionRun.algorithmRoutingLoadFailed"),
      );
      setAlgorithms(items);
      setStrategy(resolvedStrategy);
      setWeightRows(createWeightRows(resolvedStrategy.weights));
      setReason(resolvedStrategy.updated_by.reason || "");
    } catch (error) {
      if (requestId !== loadRequestIdRef.current) {
        return;
      }
      setLoadError(
        getRouterApiErrorMessage(
          error,
          t("selfEvolutionRun.algorithmRoutingLoadFailed"),
        ),
      );
    } finally {
      if (requestId === loadRequestIdRef.current) {
        setLoading(false);
      }
    }
  }, [t]);

  useEffect(() => {
    isMountedRef.current = true;
    pageTitleRef.current?.focus();
    return () => {
      isMountedRef.current = false;
    };
  }, []);

  useEffect(() => {
    void loadStrategy();
    return () => {
      loadRequestIdRef.current += 1;
    };
  }, [loadStrategy]);

  const selectedIds = useMemo(
    () => weightRows.map((row) => row.algorithmId),
    [weightRows],
  );

  const filteredAlgorithms = useMemo(() => {
    const query = searchText.trim().toLowerCase();
    if (!query) {
      return algorithms;
    }
    return algorithms.filter((algorithm) =>
      [algorithm.algorithm_id, algorithm.owner.thread_id, algorithm.status]
        .some((value) => value.toLowerCase().includes(query)),
    );
  }, [algorithms, searchText]);

  const strategyEntries = useMemo(
    () => Object.entries(strategy?.weights ?? {}),
    [strategy?.weights],
  );

  const draftWeights = useMemo(
    () => Object.fromEntries(
      weightRows.map((row) => [row.algorithmId.trim(), Math.floor(row.weight)]),
    ),
    [weightRows],
  );

  const isDraftDirty = useMemo(() => {
    const currentReason = strategy?.updated_by.reason || "";
    return (
      strategySignature(draftWeights) !== strategySignature(strategy?.weights ?? {}) ||
      reason.trim() !== currentReason
    );
  }, [draftWeights, reason, strategy]);

  const isMutating = submitting || clearing;

  const updateWeight = useCallback((index: number, value: number) => {
    const nextWeight = Math.max(0, Math.min(100, value));
    setWeightRows((previous) => {
      const next = previous.map((row) => ({ ...row }));
      next[index].weight = next.length === 1 ? 100 : nextWeight;
      if (next.length === 2) {
        next[index === 0 ? 1 : 0].weight = 100 - nextWeight;
      }
      return next;
    });
  }, []);

  const toggleAlgorithm = useCallback((algorithmId: string) => {
    setWeightRows((previous) => {
      const selectedIndex = previous.findIndex((row) => row.algorithmId === algorithmId);
      if (selectedIndex >= 0) {
        if (previous.length === 1) {
          return previous;
        }
        const remaining = previous.filter((_, index) => index !== selectedIndex);
        return remaining.map((row) => ({ ...row, weight: 100 }));
      }
      if (previous.length >= MAX_ROUTING_ALGORITHMS) {
        return previous;
      }
      if (previous.length === 0) {
        return [{ key: `row-${Date.now()}`, algorithmId, weight: 100 }];
      }
      return [
        { ...previous[0], weight: 50 },
        { key: `row-${Date.now()}`, algorithmId, weight: 50 },
      ];
    });
  }, []);

  const removeAlgorithm = useCallback((algorithmId: string) => {
    setWeightRows((previous) => {
      if (previous.length === 1) {
        return previous;
      }
      const remaining = previous.filter((row) => row.algorithmId !== algorithmId);
      return remaining.map((row) => ({ ...row, weight: 100 }));
    });
  }, []);

  const restoreLiveStrategy = useCallback(() => {
    setWeightRows(createWeightRows(strategy?.weights ?? {}));
    setReason(strategy?.updated_by.reason || "");
  }, [strategy]);

  const saveStrategy = useCallback(async () => {
    if (!weightRows.length) {
      message.error(t("selfEvolutionRun.algorithmManagementAbWeightsRequired"));
      return;
    }
    if (weightRows.some((row) => row.weight <= 0 || !Number.isFinite(row.weight))) {
      message.error(t("selfEvolutionRun.algorithmManagementAbWeightInvalid"));
      return;
    }

    setSubmitting(true);
    try {
      const nextStrategy = await putRouterABStrategy({
        weights: draftWeights,
        reason: reason.trim() || undefined,
      });
      const resolvedStrategy = requireStrategy(
        nextStrategy,
        t("selfEvolutionRun.algorithmManagementAbUpdateFailed"),
      );
      if (!isMountedRef.current) {
        return;
      }
      setStrategy(resolvedStrategy);
      setWeightRows(createWeightRows(resolvedStrategy.weights));
      setReason(resolvedStrategy.updated_by.reason || "");
      message.success(t("selfEvolutionRun.algorithmManagementAbUpdateSuccess"));
    } catch (error) {
      if (isMountedRef.current) {
        message.error(
          getRouterApiErrorMessage(
            error,
            t("selfEvolutionRun.algorithmManagementAbUpdateFailed"),
          ),
        );
      }
    } finally {
      if (isMountedRef.current) {
        setSubmitting(false);
      }
    }
  }, [draftWeights, reason, t, weightRows]);

  const clearStrategy = useCallback(() => {
    Modal.confirm({
      title: t("selfEvolutionRun.algorithmManagementAbClearTitle"),
      content: t("selfEvolutionRun.algorithmManagementAbClearContent"),
      okText: t("common.confirm"),
      okButtonProps: { danger: true },
      cancelText: t("common.cancel"),
      onOk: async () => {
        setClearing(true);
        try {
          const nextStrategy = await putRouterABStrategy({ weights: null });
          const resolvedStrategy = requireStrategy(
            nextStrategy,
            t("selfEvolutionRun.algorithmManagementAbClearFailed"),
          );
          if (!isMountedRef.current) {
            return;
          }
          setStrategy(resolvedStrategy);
          setWeightRows(createWeightRows(resolvedStrategy.weights));
          setReason(resolvedStrategy.updated_by.reason || "");
          message.success(t("selfEvolutionRun.algorithmManagementAbClearSuccess"));
        } catch (error) {
          if (isMountedRef.current) {
            message.error(
              getRouterApiErrorMessage(
                error,
                t("selfEvolutionRun.algorithmManagementAbClearFailed"),
              ),
            );
          }
          throw error;
        } finally {
          if (isMountedRef.current) {
            setClearing(false);
          }
        }
      },
    });
  }, [t]);

  const renderLoadingState = () => (
    <div className="self-evolution-routing-workspace" aria-busy="true">
      {[0, 1].map((key) => (
        <section key={key} className="self-evolution-routing-panel self-evolution-routing-loading">
          <Skeleton active paragraph={{ rows: key === 0 ? 6 : 8 }} />
        </section>
      ))}
    </div>
  );

  return (
    <div className="self-evolution-algorithm-page self-evolution-routing-page">
      <div className="self-evolution-algorithm-shell">
        <header className="self-evolution-algorithm-header">
          <div className="self-evolution-algorithm-title-group">
            <Button
              type="text"
              size="small"
              className="self-evolution-algorithm-back"
              icon={<ArrowLeftOutlined />}
              onClick={() => navigate("/self-evolution/algorithms")}
            >
              {t("common.back")}
            </Button>
            <div className="self-evolution-algorithm-title-copy">
              <h1 ref={pageTitleRef} tabIndex={-1} className="self-evolution-routing-page-title">
                {t("selfEvolutionRun.algorithmManagementAbTitle")}
              </h1>
              <Text type="secondary" className="self-evolution-algorithm-subtitle">
                {t("selfEvolutionRun.algorithmRoutingSubtitle")}
              </Text>
            </div>
          </div>
        </header>

        {loading ? renderLoadingState() : loadError ? (
          <section className="self-evolution-routing-panel self-evolution-routing-error" role="alert">
            <WarningOutlined className="self-evolution-routing-error-icon" aria-hidden="true" />
            <h2>{t("selfEvolutionRun.algorithmRoutingLoadFailed")}</h2>
            <p>{loadError}</p>
            <Button
              type="primary"
              size="small"
              icon={<ReloadOutlined />}
              onClick={() => void loadStrategy()}
            >
              {t("selfEvolutionRun.retry")}
            </Button>
          </section>
        ) : (
          <>
            <section className="self-evolution-routing-live" aria-labelledby="routing-live-title">
              <div className="self-evolution-routing-live-copy">
                <div className="self-evolution-routing-live-heading">
                  <h2 id="routing-live-title">
                    {t("selfEvolutionRun.algorithmRoutingCurrentTitle")}
                  </h2>
                  <Tag color={strategy?.active ? "success" : "default"}>
                    {strategy?.active
                      ? t("selfEvolutionRun.algorithmManagementAbActive")
                      : t("selfEvolutionRun.algorithmManagementAbInactive")}
                  </Tag>
                </div>
                <div className="self-evolution-routing-live-values">
                  {strategyEntries.length ? strategyEntries.map(([algorithmId, weight]) => (
                    <span key={algorithmId} className="self-evolution-routing-live-value">
                      <span className="self-evolution-algorithm-mono">{algorithmId}</span>
                      <strong>{weight}%</strong>
                    </span>
                  )) : (
                    <Text type="secondary">
                      {t("selfEvolutionRun.algorithmManagementAbNoWeights")}
                    </Text>
                  )}
                </div>
              </div>
              {strategy?.updated_by.reason ? (
                <Text type="secondary" className="self-evolution-routing-live-reason">
                  {t("selfEvolutionRun.algorithmManagementAbReason", {
                    reason: strategy.updated_by.reason,
                  })}
                </Text>
              ) : null}
              <Button
                danger
                size="small"
                disabled={!strategy?.active || submitting}
                loading={clearing}
                onClick={clearStrategy}
              >
                {t("selfEvolutionRun.algorithmManagementAbClear")}
              </Button>
            </section>

            <div className="self-evolution-routing-workspace">
              <section className="self-evolution-routing-panel" aria-labelledby="routing-catalog-title">
                <div className="self-evolution-routing-panel-header">
                  <div>
                    <h2 id="routing-catalog-title">
                      {t("selfEvolutionRun.algorithmRoutingCatalogTitle")}
                    </h2>
                    <Text type="secondary">
                      {t("selfEvolutionRun.algorithmRoutingCatalogDescription")}
                    </Text>
                  </div>
                  <Text type="secondary" className="self-evolution-routing-service-count">
                    {t("selfEvolutionRun.algorithmRoutingServicesCount", {
                      count: algorithms.length,
                    })}
                  </Text>
                </div>
                <div className="self-evolution-routing-catalog-toolbar">
                  <Input
                    allowClear
                    size="small"
                    prefix={<SearchOutlined />}
                    value={searchText}
                    placeholder={t("selfEvolutionRun.algorithmRoutingSearchPlaceholder")}
                    onChange={(event: ChangeEvent<HTMLInputElement>) => setSearchText(event.target.value)}
                  />
                  <span className="self-evolution-routing-selected-count" role="status" aria-live="polite">
                    {t("selfEvolutionRun.algorithmRoutingSelectedCount", {
                      selected: selectedIds.length,
                      max: MAX_ROUTING_ALGORITHMS,
                    })}
                  </span>
                </div>
                <div className="self-evolution-routing-catalog-body">
                  {filteredAlgorithms.length ? (
                    <ul className="self-evolution-routing-service-list">
                      {filteredAlgorithms.map((algorithm) => {
                        const selected = selectedIds.includes(algorithm.algorithm_id);
                        const selectionFull = !selected && selectedIds.length >= MAX_ROUTING_ALGORITHMS;
                        const removalLocked = selected && selectedIds.length === 1;
                        const selectionBlocked = selectionFull || removalLocked;
                        return (
                          <li key={algorithm.algorithm_id}>
                            <button
                              type="button"
                              className={`self-evolution-routing-service${selected ? " is-selected" : ""}${selectionBlocked ? " is-selection-blocked" : ""}`}
                              aria-pressed={selected}
                              aria-disabled={selectionBlocked || isMutating}
                              disabled={isMutating}
                              title={selectionFull
                                ? t("selfEvolutionRun.algorithmRoutingSelectionLimit")
                                : removalLocked
                                  ? t("selfEvolutionRun.algorithmRoutingMinimumOne")
                                  : undefined}
                              onClick={() => {
                                if (removalLocked) {
                                  message.info(t("selfEvolutionRun.algorithmRoutingMinimumOne"));
                                  return;
                                }
                                if (selectionFull) {
                                  message.info(t("selfEvolutionRun.algorithmRoutingSelectionLimit"));
                                  return;
                                }
                                toggleAlgorithm(algorithm.algorithm_id);
                              }}
                            >
                              <span className="self-evolution-routing-service-head">
                                <span className="self-evolution-algorithm-mono">
                                  {algorithm.algorithm_id}
                                </span>
                                <Tag color={statusColor(algorithm.status)}>
                                  {algorithm.status || "-"}
                                </Tag>
                              </span>
                              <span className="self-evolution-routing-service-meta">
                                <span>
                                  <small>{t("selfEvolutionRun.algorithmRoutingHealthyInstances")}</small>
                                  <strong>{algorithm.healthy_instances}/{algorithm.instance_count}</strong>
                                </span>
                                <span>
                                  <small>{t("selfEvolutionRun.algorithmRoutingOwnerThread")}</small>
                                  <strong className="self-evolution-algorithm-mono">
                                    {algorithm.owner.thread_id || "-"}
                                  </strong>
                                </span>
                              </span>
                              <span className="self-evolution-routing-service-action">
                                {selected ? <CheckOutlined /> : <PlusOutlined />}
                                {selected
                                  ? t("selfEvolutionRun.algorithmRoutingJoined")
                                  : t("selfEvolutionRun.algorithmRoutingJoin")}
                              </span>
                            </button>
                          </li>
                        );
                      })}
                    </ul>
                  ) : (
                    <Empty
                      image={Empty.PRESENTED_IMAGE_SIMPLE}
                      description={searchText.trim()
                        ? t("selfEvolutionRun.algorithmRoutingNoSearchResults")
                        : t("selfEvolutionRun.algorithmManagementNoAlgorithms")}
                    />
                  )}
                </div>
              </section>

              <section className="self-evolution-routing-panel self-evolution-routing-draft" aria-labelledby="routing-draft-title">
                <div className="self-evolution-routing-panel-header">
                  <div>
                    <h2 id="routing-draft-title">
                      {t("selfEvolutionRun.algorithmRoutingDraftTitle")}
                    </h2>
                    <Text type="secondary">
                      {t("selfEvolutionRun.algorithmRoutingDraftDescription")}
                    </Text>
                  </div>
                  <Tag color={isDraftDirty ? "warning" : "default"}>
                    {isDraftDirty
                      ? t("selfEvolutionRun.algorithmRoutingDraftChanged")
                      : t("selfEvolutionRun.algorithmRoutingDraftSynced")}
                  </Tag>
                </div>

                <div className="self-evolution-routing-editor-body">
                  {weightRows.length ? (
                    <>
                      <div className="self-evolution-routing-preview" aria-label={t("selfEvolutionRun.algorithmRoutingTrafficPreview")}>
                        <div className="self-evolution-routing-preview-head">
                          <Text strong>{t("selfEvolutionRun.algorithmRoutingTrafficPreview")}</Text>
                          <Text type="secondary">100%</Text>
                        </div>
                        <div className="self-evolution-routing-preview-bar">
                          {weightRows.map((row, index) => (
                            <div
                              key={row.key}
                              className={`self-evolution-routing-preview-segment tone-${index}`}
                              style={{ width: `${row.weight}%` }}
                              title={`${row.algorithmId}: ${row.weight}%`}
                            >
                              <span>{row.weight}%</span>
                            </div>
                          ))}
                        </div>
                        <div className="self-evolution-routing-preview-legend">
                          {weightRows.map((row, index) => (
                            <span key={row.key} className={`tone-${index}`}>
                              <i aria-hidden="true" />
                              <span className="self-evolution-algorithm-mono">{row.algorithmId}</span>
                            </span>
                          ))}
                        </div>
                      </div>

                      <div className="self-evolution-routing-rows">
                        {weightRows.map((row, index) => {
                          const algorithm = algorithms.find(
                            (item) => item.algorithm_id === row.algorithmId,
                          );
                          return (
                            <article key={row.key} className={`self-evolution-routing-allocation tone-${index}`}>
                              <div className="self-evolution-routing-allocation-head">
                                <div>
                                  <span className="self-evolution-routing-allocation-label">
                                    {t("selfEvolutionRun.algorithmRoutingTrafficShare")}
                                  </span>
                                  <strong
                                    className="self-evolution-algorithm-mono"
                                    title={row.algorithmId}
                                  >
                                    {row.algorithmId}
                                  </strong>
                                  {algorithm ? (
                                    <Text type="secondary">
                                      {algorithm.healthy_instances}/{algorithm.instance_count} {t("selfEvolutionRun.algorithmRoutingHealthySuffix")}
                                    </Text>
                                  ) : null}
                                </div>
                                <div className="self-evolution-routing-allocation-actions">
                                  <span className="self-evolution-routing-weight-value">
                                    {row.weight}<small>%</small>
                                  </span>
                                  {weightRows.length > 1 ? (
                                    <Button
                                      type="text"
                                      size="small"
                                      danger
                                      aria-label={`${t("common.delete")} ${row.algorithmId}`}
                                      icon={<DeleteOutlined />}
                                      disabled={isMutating}
                                      onClick={() => removeAlgorithm(row.algorithmId)}
                                    />
                                  ) : null}
                                </div>
                              </div>
                              <div className="self-evolution-routing-weight-control">
                                <Slider
                                  min={weightRows.length === 2 ? 1 : 0}
                                  max={weightRows.length === 2 ? 99 : 100}
                                  aria-label={`${row.algorithmId} ${t("selfEvolutionRun.algorithmManagementAbWeight")}`}
                                  value={row.weight}
                                  disabled={weightRows.length === 1 || isMutating}
                                  onChange={(value: number) => updateWeight(index, value)}
                                />
                                <InputNumber
                                  size="small"
                                  min={weightRows.length === 2 ? 1 : 0}
                                  max={weightRows.length === 2 ? 99 : 100}
                                  aria-label={`${row.algorithmId} ${t("selfEvolutionRun.algorithmManagementAbWeight")}`}
                                  value={row.weight}
                                  formatter={(value: number | undefined) => `${value ?? 0}%`}
                                  parser={(value?: string) => Number(value?.replace("%", ""))}
                                  disabled={weightRows.length === 1 || isMutating}
                                  onChange={(value: number | null) =>
                                    updateWeight(index, typeof value === "number" ? value : 0)
                                  }
                                />
                              </div>
                            </article>
                          );
                        })}
                      </div>

                      <div className="self-evolution-routing-field">
                        <label htmlFor="routing-change-reason">
                          {t("selfEvolutionRun.algorithmRoutingReasonLabel")}
                        </label>
                        <Input.TextArea
                          id="routing-change-reason"
                          rows={2}
                          value={reason}
                          disabled={isMutating}
                          placeholder={t("selfEvolutionRun.algorithmManagementAbReasonPlaceholder")}
                          onChange={(event: ChangeEvent<HTMLTextAreaElement>) =>
                            setReason(event.target.value)
                          }
                        />
                      </div>
                    </>
                  ) : (
                    <div className="self-evolution-routing-draft-empty">
                      <Empty
                        image={Empty.PRESENTED_IMAGE_SIMPLE}
                        description={(
                          <div>
                            <strong>{t("selfEvolutionRun.algorithmRoutingDraftEmpty")}</strong>
                            <span>{t("selfEvolutionRun.algorithmRoutingDraftEmptyDescription")}</span>
                          </div>
                        )}
                      />
                    </div>
                  )}
                </div>

                <div className="self-evolution-routing-panel-footer">
                  <Button
                    size="small"
                    disabled={!isDraftDirty || isMutating}
                    onClick={restoreLiveStrategy}
                  >
                    {t("selfEvolutionRun.algorithmRoutingResetDraft")}
                  </Button>
                  <Button
                    type="primary"
                    size="small"
                    loading={submitting}
                    disabled={!weightRows.length || !isDraftDirty || clearing}
                    onClick={() => void saveStrategy()}
                  >
                    {t("selfEvolutionRun.algorithmRoutingSave")}
                  </Button>
                </div>
              </section>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

export default RoutingStrategyManagementPage;
