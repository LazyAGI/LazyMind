import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  closestCenter,
  useSensor,
  useSensors,
} from "@dnd-kit/core";
import type { DragEndEvent } from "@dnd-kit/core";
import {
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import {
  DeleteOutlined,
  HolderOutlined,
  LeftOutlined,
  RightOutlined,
  SettingOutlined,
  ClearOutlined,
} from "@ant-design/icons";
import {
  Alert,
  Button,
  Descriptions,
  Empty,
  Modal,
  Popconfirm,
  Progress,
  Skeleton,
  Tag,
  message,
} from "antd";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import {
  deletePreferenceMemory,
  isPreferenceOrganizing,
  getPreferenceMemory,
  listPreferenceMemories,
  reorderPreferenceMemories,
  type PreferenceMemoryDetail,
  type PreferenceMemoryItem,
  type PreferenceMemoryList,
} from "../../currentMemoryApi";
import {
  getPreferenceBudgetTone,
  preferenceOrganizerPresentation,
  isCurrentMemoryConflict,
  isCurrentMemoryResourceNotFound,
  mergePreferenceOrderWithLatest,
  movePreferenceItem,
} from "../../currentMemoryViewModel";
import { getMemorySourceLabelKey } from "../../memorySourceLabels";
import SafeReferenceMarkdown from "./SafeReferenceMarkdown";

import { usePreferenceOrganizer } from "../../hooks/usePreferenceOrganizer";

const PAGE_SIZE = 5;

interface SortablePreferenceRowProps {
  deleting: boolean;
  disabled: boolean;
  index: number;
  item: PreferenceMemoryItem;
  total: number;
  onDelete: (item: PreferenceMemoryItem) => Promise<void>;
  onOpen: (item: PreferenceMemoryItem) => void;
}

function SortablePreferenceRow({
  deleting,
  disabled,
  index,
  item,
  total,
  onDelete,
  onOpen,
}: SortablePreferenceRowProps) {
  const { i18n, t } = useTranslation();
  const {
    attributes,
    isDragging,
    listeners,
    setNodeRef,
    transform,
    transition,
  } = useSortable({ disabled, id: item.name });

  const updatedAt = useMemo(() => {
    const date = new Date(item.updatedAt);
    if (Number.isNaN(date.getTime())) {
      return item.updatedAt;
    }
    return new Intl.DateTimeFormat(
      i18n.resolvedLanguage || i18n.language,
      { dateStyle: "medium" },
    ).format(date);
  }, [i18n.language, i18n.resolvedLanguage, item.updatedAt]);

  return (
    <article
      ref={setNodeRef}
      className={[
        "memory-preference-item",
        isDragging ? "is-dragging" : "",
      ].filter(Boolean).join(" ")}
      style={{
        transform: CSS.Transform.toString(transform),
        transition,
      }}
    >
      <Button
        {...attributes}
        {...listeners}
        aria-label={t("admin.memoryPreferenceReorderHandle", {
          name: item.name,
          position: index + 1,
          total,
        })}
        className="memory-preference-drag-handle"
        disabled={disabled}
        icon={<HolderOutlined />}
        size="small"
        type="text"
      />
      <button
        type="button"
        className="memory-preference-item-main"
        onClick={() => onOpen(item)}
      >
        <span className="memory-preference-item-icon">
          <SettingOutlined />
        </span>
        <span className="memory-preference-item-copy">
          <strong>{item.name}</strong>
          <span>{item.summary}</span>
          <small>
            {t("admin.memoryPreferenceUpdatedAt", { time: updatedAt })}
          </small>
        </span>
        <RightOutlined />
      </button>
      <Popconfirm
        cancelText={t("common.cancel")}
        description={t("admin.memoryPreferenceDeleteConfirmDescription")}
        okButtonProps={{ danger: true, loading: deleting }}
        okText={t("common.delete")}
        title={t("admin.memoryPreferenceDeleteConfirmTitle", {
          name: item.name,
        })}
        onConfirm={() => onDelete(item)}
      >
        <Button
          aria-label={t("admin.memoryPreferenceDelete", {
            name: item.name,
          })}
          className="memory-preference-delete"
          danger
          disabled={disabled || deleting}
          icon={<DeleteOutlined />}
          loading={deleting}
          size="small"
          type="text"
        />
      </Popconfirm>
    </article>
  );
}

export default function PreferenceMemorySection() {
  const { i18n, t } = useTranslation();
  const detailRequestIdRef = useRef(0);
  const selectedDetailRef = useRef<PreferenceMemoryItem | null>(null);
  const [list, setList] = useState<PreferenceMemoryList | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [ordering, setOrdering] = useState(false);
  const [orderConflict, setOrderConflict] = useState(false);
  const [orderError, setOrderError] = useState("");
  const [preDragList, setPreDragList] =
    useState<PreferenceMemoryList | null>(null);
  const [pageIndex, setPageIndex] = useState(0);
  const [deletingNames, setDeletingNames] = useState<Set<string>>(new Set());
  const [detailOpen, setDetailOpen] = useState(false);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState("");
  const [detail, setDetail] = useState<PreferenceMemoryDetail | null>(null);

  const sensors = useSensors(
    useSensor(PointerSensor, {
      activationConstraint: { distance: 8 },
    }),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    }),
  );

  const refresh = useCallback(async () => {
    setLoading(true);
    setList((current) => current ? { ...current, budget: undefined } : current);
    setLoadError("");
    try {
      setList(await listPreferenceMemories());
      setOrderConflict(false);
      setOrderError("");
      setPreDragList(null);
    } catch (error) {
      if (isCurrentMemoryResourceNotFound(error)) {
        setList(null);
      } else {
        console.error("Load Preference memory failed:", error);
        setLoadError(getLocalizedErrorMessage(error));
      }
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const organizer = usePreferenceOrganizer((completed) => {
    void refresh();
    if (detailOpen && selectedDetailRef.current) void openDetail(selectedDetailRef.current);
    const presentation = preferenceOrganizerPresentation(completed);
    if (presentation) message[presentation.tone](t(presentation.key));
  });
  const organizerPresentation = preferenceOrganizerPresentation(organizer.task);
  const organizerLabel = organizer.active && organizerPresentation
    ? organizerPresentation.key : "admin.memoryPreferenceOrganize";
  const organizerResultLabel = !organizer.active ? organizerPresentation?.key : null;
  const submitOrganizer = async () => {
    try { await organizer.submit(); }
    catch (error) { message.error(getLocalizedErrorMessage(error)); }
  };

  const pageCount = Math.max(
    1,
    Math.ceil((list?.items.length || 0) / PAGE_SIZE),
  );
  const visibleItems = useMemo(() => {
    const start = pageIndex * PAGE_SIZE;
    return list?.items.slice(start, start + PAGE_SIZE) || [];
  }, [list?.items, pageIndex]);
  const budget = list?.budget;
  const budgetTone = budget ? getPreferenceBudgetTone(budget.usedChars, budget.maxChars) : "normal";

  useEffect(() => {
    setPageIndex((current) => Math.min(current, pageCount - 1));
  }, [pageCount]);

  const applyOrder = async (
    optimisticItems: PreferenceMemoryItem[],
    expectedEtag: string,
    rollbackList?: PreferenceMemoryList,
  ) => {
    setOrdering(true);
    setOrderError("");
    setOrderConflict(false);
    try {
      const updated = await reorderPreferenceMemories(
        optimisticItems.map((item) => item.name),
        expectedEtag,
      );
      setList(updated);
      setPreDragList(null);
    } catch (error) {
      console.error("Reorder Preference memory failed:", error);
      if (isPreferenceOrganizing(error)) {
        if (rollbackList) setList(rollbackList);
        setPreDragList(null);
        setOrderConflict(false);
        message.info(t("admin.memoryPreferenceOrganizeRunning"));
        void organizer.refresh();
      } else if (isCurrentMemoryConflict(error)) {
        setOrderConflict(true);
      } else {
        if (rollbackList) {
          setList(rollbackList);
        }
        setOrderError(getLocalizedErrorMessage(error));
        setPreDragList(null);
      }
    } finally {
      setOrdering(false);
    }
  };

  const handleDragEnd = (event: DragEndEvent) => {
    if (!list || organizer.running || ordering || orderConflict || !event.over) {
      return;
    }
    const activeName = String(event.active.id);
    const overName = String(event.over.id);
    if (activeName === overName) {
      return;
    }
    const previousItems = list.items;
    const optimisticItems = movePreferenceItem(
      previousItems,
      activeName,
      overName,
    );
    setPreDragList(list);
    setList({ ...list, items: optimisticItems, budget: undefined });
    void applyOrder(optimisticItems, list.etag, list);
  };

  const resubmitRetainedOrder = async () => {
    if (!list || organizer.running || ordering) {
      return;
    }
    setOrdering(true);
    setOrderError("");
    try {
      const latest = await listPreferenceMemories();
      const rebasedItems = mergePreferenceOrderWithLatest(
        list.items,
        latest.items,
      );
      setPreDragList(latest);
      setList({ ...latest, items: rebasedItems, budget: undefined });
      setOrdering(false);
      await applyOrder(rebasedItems, latest.etag, latest);
    } catch (error) {
      console.error("Reload Preference order failed:", error);
      setOrdering(false);
      if (isCurrentMemoryConflict(error)) {
        setOrderConflict(true);
      } else {
        setOrderError(getLocalizedErrorMessage(error));
      }
    }
  };

  const undoLocalOrder = () => {
    if (preDragList) {
      setList({ ...preDragList, budget: undefined });
    }
    setPreDragList(null);
    setOrderConflict(false);
    setOrderError("");
    void refresh();
  };

  const openDetail = async (item: PreferenceMemoryItem) => {
    selectedDetailRef.current = item;
    const requestId = detailRequestIdRef.current + 1;
    detailRequestIdRef.current = requestId;
    setDetailOpen(true);
    setDetail(null);
    setDetailLoading(true);
    setDetailError("");
    try {
      const nextDetail = await getPreferenceMemory(item.name);
      if (detailRequestIdRef.current === requestId) {
        setDetail(nextDetail);
      }
    } catch (error) {
      if (detailRequestIdRef.current === requestId) {
        console.error("Load Preference detail failed:", error);
        setDetailError(getLocalizedErrorMessage(error));
      }
    } finally {
      if (detailRequestIdRef.current === requestId) {
        setDetailLoading(false);
      }
    }
  };

  const deleteItem = async (item: PreferenceMemoryItem) => {
    if (organizer.running || ordering || orderConflict) {
      return;
    }
    setDeletingNames((current) => new Set(current).add(item.name));
    try {
      await deletePreferenceMemory(item.name);
      setList((current) =>
        current
          ? {
              ...current,
              items: current.items.filter(
                (candidate) => candidate.name !== item.name,
              ),
              totalSize: Math.max(0, current.totalSize - 1),
              budget: undefined,
            }
          : current,
      );
      setOrderConflict(false);
      setOrderError("");
      setPreDragList(null);
      if (detail?.item.name === item.name) {
        setDetailOpen(false);
        setDetail(null);
      }
      message.success(t("admin.memoryPreferenceDeleteSuccess"));

      try {
        setList(await listPreferenceMemories());
      } catch (refreshError) {
        console.error(
          "Refresh Preference memory after delete failed:",
          refreshError,
        );
        message.warning(
          t("admin.memoryPreferenceRefreshAfterDeleteFailed"),
        );
      }
    } catch (error) {
      console.error("Delete Preference memory failed:", error);
      if (isPreferenceOrganizing(error)) {
        message.info(t("admin.memoryPreferenceOrganizeRunning"));
        void organizer.refresh();
      } else message.error(getLocalizedErrorMessage(error));
    } finally {
      setDeletingNames((current) => {
        const next = new Set(current);
        next.delete(item.name);
        return next;
      });
    }
  };

  const detailUpdatedAt = useMemo(() => {
    if (!detail?.item.updatedAt) {
      return "";
    }
    const date = new Date(detail.item.updatedAt);
    if (Number.isNaN(date.getTime())) {
      return detail.item.updatedAt;
    }
    return new Intl.DateTimeFormat(
      i18n.resolvedLanguage || i18n.language,
      { dateStyle: "medium", timeStyle: "short" },
    ).format(date);
  }, [detail?.item.updatedAt, i18n.language, i18n.resolvedLanguage]);

  const formatReferenceTimestamp = useCallback(
    (value: string) => {
      const date = new Date(value);
      if (Number.isNaN(date.getTime())) {
        return value;
      }
      return new Intl.DateTimeFormat(
        i18n.resolvedLanguage || i18n.language,
        { dateStyle: "medium", timeStyle: "short" },
      ).format(date);
    },
    [i18n.language, i18n.resolvedLanguage],
  );

  const getSourceLabel = useCallback(
    (sourceKind: string) => {
      const translationKey = getMemorySourceLabelKey(sourceKind);
      return translationKey ? t(translationKey) : sourceKind;
    },
    [t],
  );

  return (
    <section
      className="memory-preference-section"
      aria-label={t("admin.memoryPreferenceTitle")}
    >
      <div className="memory-current-section-heading">
        <span className="memory-current-section-icon is-preference">
          <SettingOutlined />
        </span>
        <div>
          <h3>{t("admin.memoryPreferenceTitle")}</h3>
          <p>{t("admin.memoryPreferenceDescription")}</p>
        </div>
        <Button icon={<ClearOutlined />} loading={organizer.submitting}
          disabled={organizer.running || !list?.items.length} onClick={() => void submitOrganizer()}>
          {t(organizerLabel)}
        </Button>
        <span className="memory-current-section-count">
          {t("admin.memoryPreferenceTotal", {
            count: list?.totalSize || 0,
          })}
        </span>
      </div>

      {organizerResultLabel ? <p role="status">{t(organizerResultLabel)}</p> : null}
      {organizer.error ? <Alert type="warning" showIcon message={t("admin.memoryPreferenceOrganizeStatusFailed")}
        action={<Button size="small" onClick={() => void organizer.refresh()}>{t("common.retry")}</Button>} /> : null}
      {budget ? (
        <div
          className={`memory-preference-usage is-${budgetTone}`}
          aria-label={t("admin.memoryPreferenceBudgetAria", {
            max: budget.maxChars,
            used: budget.usedChars,
          })}
        >
          <div className="memory-preference-usage-copy">
            <span>{t("admin.memoryPreferenceCharacterBudget")}</span>
            <strong>
              {budget.usedChars} / {budget.maxChars}
            </strong>
          </div>
          <Progress
            percent={Math.min(100, budget.usedChars / budget.maxChars * 100)}
            showInfo={false}
            size="small"
            status={budgetTone === "error" ? "exception" : "normal"}
            strokeColor={
              budgetTone === "warning" || budgetTone === "full" ? "#d99218" : undefined
            }
          />
          <small>{t("admin.memoryPreferenceBudgetDescription")}</small>
          {budgetTone === "full" ? <span>{t("admin.memoryPreferenceBudgetFull")}</span> : null}
        </div>
      ) : list ? <p role="status">{t("admin.memoryPreferenceBudgetStale")}{" "}
        <Button size="small" disabled={loading || ordering} onClick={() => void refresh()}>{t("common.retry")}</Button>
      </p> : null}

      {budgetTone === "error" ? (
        <Alert
          className="memory-preference-capacity-alert"
          description={t("admin.memoryPreferenceOverLimitDescription", {
            max: budget?.maxChars,
          })}
          message={t("admin.memoryPreferenceOverLimitTitle")}
          showIcon
          type="warning"
        />
      ) : null}

      {orderConflict ? (
        <Alert
          action={
            <div className="memory-current-conflict-actions">
              <Button
                disabled={organizer.running || ordering}
                size="small"
                onClick={() => void refresh()}
              >
                {t("admin.memoryPreferenceReloadOrder")}
              </Button>
              <Button
                disabled={organizer.running || ordering}
                loading={ordering}
                size="small"
                type="primary"
                onClick={() => void resubmitRetainedOrder()}
              >
                {t("admin.memoryPreferenceResubmitOrder")}
              </Button>
              <Button
                disabled={ordering || !preDragList}
                size="small"
                onClick={undoLocalOrder}
              >
                {t("admin.memoryPreferenceUndoOrder")}
              </Button>
            </div>
          }
          className="memory-preference-order-alert"
          description={t("admin.memoryPreferenceConflictDescription")}
          message={t("admin.memoryPreferenceConflictTitle")}
          showIcon
          type="warning"
        />
      ) : null}
      {orderError ? (
        <Alert
          className="memory-preference-order-alert"
          closable
          description={orderError}
          message={t("admin.memoryPreferenceReorderFailed")}
          showIcon
          type="error"
          onClose={() => setOrderError("")}
        />
      ) : null}

      {loading && !list ? (
        <div className="memory-preference-state" aria-busy="true">
          <Skeleton active paragraph={{ rows: 4 }} />
        </div>
      ) : loadError && !list ? (
        <Alert
          action={
            <Button size="small" onClick={() => void refresh()}>
              {t("common.retry")}
            </Button>
          }
          className="memory-preference-state"
          description={loadError}
          message={t("admin.memoryPreferenceLoadFailed")}
          showIcon
          type="error"
        />
      ) : !list?.items.length ? (
        <div className="memory-preference-state">
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description={t("admin.memoryPreferenceEmpty")}
          />
        </div>
      ) : (
        <>
          <DndContext
            collisionDetection={closestCenter}
            sensors={sensors}
            onDragEnd={handleDragEnd}
          >
            <SortableContext
              items={visibleItems.map((item) => item.name)}
              strategy={verticalListSortingStrategy}
            >
              <div
                className="memory-preference-list"
                aria-busy={ordering}
              >
                {visibleItems.map((item, index) => (
                  <SortablePreferenceRow
                    deleting={deletingNames.has(item.name)}
                    disabled={organizer.running || ordering || orderConflict}
                    index={pageIndex * PAGE_SIZE + index}
                    item={item}
                    key={item.name}
                    total={list.items.length}
                    onDelete={deleteItem}
                    onOpen={(selected) => void openDetail(selected)}
                  />
                ))}
              </div>
            </SortableContext>
          </DndContext>

          <div className="memory-preference-pagination">
            <span>
              {t("admin.memoryPreferencePage", {
                count: visibleItems.length,
                page: pageIndex + 1,
                total: list.totalSize,
              })}
            </span>
            <div className="memory-preference-pagination-actions">
              <Button
                aria-label={t("common.previous")}
                disabled={pageIndex === 0 || ordering}
                icon={<LeftOutlined />}
                size="small"
                onClick={() =>
                  setPageIndex((current) => Math.max(0, current - 1))
                }
              />
              <Button
                aria-label={t("common.next")}
                disabled={pageIndex >= pageCount - 1 || ordering}
                icon={<RightOutlined />}
                size="small"
                onClick={() =>
                  setPageIndex((current) =>
                    Math.min(pageCount - 1, current + 1),
                  )
                }
              >
                {t("common.next")}
              </Button>
            </div>
          </div>
        </>
      )}

      <Modal
        destroyOnHidden
        footer={null}
        open={detailOpen}
        title={t("admin.memoryPreferenceDetailTitle")}
        width={760}
        onCancel={() => {
          detailRequestIdRef.current += 1;
          setDetailOpen(false);
          setDetailError("");
        }}
      >
        {detailLoading ? (
          <Skeleton active paragraph={{ rows: 6 }} />
        ) : detailError ? (
          <Alert
            description={detailError}
            message={t("admin.memoryPreferenceDetailLoadFailed")}
            showIcon
            type="error"
          />
        ) : detail ? (
          <div className="memory-preference-detail">
            <div className="memory-preference-detail-hero">
              <span className="memory-preference-item-icon">
                <SettingOutlined />
              </span>
              <div>
                <h4>{detail.item.name}</h4>
                <p>{detail.item.summary}</p>
                <small>
                  {t("admin.memoryPreferenceUpdatedAt", {
                    time: detailUpdatedAt,
                  })}
                </small>
              </div>
            </div>

            {detail.referenceStatus === "missing" || !detail.reference ? (
              <Alert
                description={t(
                  "admin.memoryPreferenceReferenceMissingDescription",
                )}
                message={t("admin.memoryPreferenceReferenceMissing")}
                showIcon
                type="warning"
              />
            ) : (
              <>
                <Descriptions bordered column={1} size="small">
                  <Descriptions.Item
                    label={t("admin.memoryPreferenceReferenceName")}
                  >
                    {detail.reference.name}
                  </Descriptions.Item>
                  <Descriptions.Item
                    label={t("admin.memoryPreferenceReferenceSource")}
                  >
                    <Tag bordered={false}>
                      {getSourceLabel(detail.reference.source.kind)}
                    </Tag>
                    <code>{detail.reference.source.conversationId}</code>
                  </Descriptions.Item>
                  <Descriptions.Item
                    label={t("admin.memoryPreferenceReferenceSummary")}
                  >
                    {detail.reference.summary}
                  </Descriptions.Item>
                  <Descriptions.Item
                    label={t("admin.memoryPreferenceReferenceCreatedAt")}
                  >
                    {formatReferenceTimestamp(detail.reference.createdAt)}
                  </Descriptions.Item>
                  <Descriptions.Item
                    label={t("admin.memoryPreferenceReferenceUpdatedAt")}
                  >
                    {formatReferenceTimestamp(detail.reference.updatedAt)}
                  </Descriptions.Item>
                </Descriptions>
                <section className="memory-preference-reference-section">
                  <h5>
                    {t("admin.memoryPreferenceApplicationScenarios")}
                  </h5>
                  <SafeReferenceMarkdown
                    content={detail.reference.applicationScenarios}
                  />
                </section>
                <section className="memory-preference-reference-section">
                  <h5>{t("admin.memoryPreferenceDetails")}</h5>
                  <SafeReferenceMarkdown
                    content={detail.reference.preferenceDetails}
                  />
                </section>
                <section className="memory-preference-reference-section">
                  <h5>{t("admin.memoryPreferenceReason")}</h5>
                  <SafeReferenceMarkdown content={detail.reference.reason} />
                </section>
              </>
            )}
          </div>
        ) : null}
      </Modal>
    </section>
  );
}
