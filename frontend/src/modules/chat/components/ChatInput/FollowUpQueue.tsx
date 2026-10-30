import {
  useMemo,
  useState,
  type ChangeEvent,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import { Button, Input, Tooltip } from "antd";
import {
  ClockCircleOutlined,
  DeleteOutlined,
  EditOutlined,
  EnterOutlined,
  ExclamationCircleOutlined,
  HolderOutlined,
  LoadingOutlined,
  RedoOutlined,
} from "@ant-design/icons";
import {
  DndContext,
  PointerSensor,
  closestCenter,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  arrayMove,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { useTranslation } from "react-i18next";

import type { ConversationFollowUp } from "../../utils/request";

interface FollowUpQueueProps {
  items: ConversationFollowUp[];
  isStreaming: boolean;
  onSteer: (item: ConversationFollowUp) => void;
  onRetry: (item: ConversationFollowUp) => void;
  onRemove: (item: ConversationFollowUp) => void;
  onUpdate: (item: ConversationFollowUp, text: string) => void;
  onReorder: (orderedIds: string[]) => void;
}

function SortableFollowUp({
  item,
  children,
}: {
  item: ConversationFollowUp;
  children: (handle: ReactNode) => ReactNode;
}) {
  const sortable = useSortable({ id: item.id, disabled: item.status !== "pending" });
  const style = {
    transform: CSS.Transform.toString(sortable.transform),
    transition: sortable.transition,
  };
  const handle = item.status === "pending" || item.status === "steering" ? (
    <button
      type="button"
      className="follow-up-drag"
      aria-label="Reorder queued message"
      disabled={item.status === "steering"}
      ref={sortable.setActivatorNodeRef}
      {...sortable.attributes}
      {...sortable.listeners}
    >
      <HolderOutlined />
    </button>
  ) : null;
  return (
    <div ref={sortable.setNodeRef} style={style} className="follow-up-sortable">
      {children(handle)}
    </div>
  );
}

export default function FollowUpQueue({
  items,
  isStreaming,
  onSteer,
  onRetry,
  onRemove,
  onUpdate,
  onReorder,
}: FollowUpQueueProps) {
  const { t } = useTranslation();
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }));
  const [editingID, setEditingID] = useState("");
  const [editingText, setEditingText] = useState("");

  const pendingItems = useMemo(
    () => items.filter((item) => item.status === "pending").sort((a, b) => a.sort_order - b.sort_order),
    [items],
  );
  const displayItems = useMemo(() => [
    ...pendingItems,
    ...items.filter((item) => item.status !== "pending" && item.status !== "accepted"),
  ], [items, pendingItems]);

  if (!displayItems.length) return null;

  const statusLabel = (item: ConversationFollowUp) => {
    switch (item.status) {
      case "steering": return t("chat.followUpSteering");
      case "failed": return t("chat.followUpFailed");
      case "dispatching": return t("chat.followUpDispatching");
      default: return t("chat.followUpPending");
    }
  };

  const statusIcon = (item: ConversationFollowUp) => {
    switch (item.status) {
      case "steering":
      case "dispatching":
        return <LoadingOutlined spin />;
      case "failed":
        return <ExclamationCircleOutlined />;
      default:
        return <ClockCircleOutlined />;
    }
  };

  const dragEnded = (event: DragEndEvent) => {
    if (!event.over || event.active.id === event.over.id) return;
    const oldIndex = pendingItems.findIndex((item) => item.id === event.active.id);
    const newIndex = pendingItems.findIndex((item) => item.id === event.over?.id);
    if (oldIndex < 0 || newIndex < 0) return;
    onReorder(arrayMove(pendingItems, oldIndex, newIndex).map((item) => item.id));
  };

  return (
    <section className="follow-up-queue" aria-label={t("chat.followUpQueueTitle")} aria-live="polite">
      <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={dragEnded}>
        <SortableContext items={pendingItems.map((item) => item.id)} strategy={verticalListSortingStrategy}>
          {displayItems.map((item) => (
            <SortableFollowUp key={item.id} item={item}>
              {(handle) => (
                <div className={`follow-up-item is-${item.status}`}>
                  {handle}
                  <div className="follow-up-copy">
                    {editingID === item.id ? (
                      <Input.TextArea
                        autoFocus
                        value={editingText}
                        autoSize={{ minRows: 1, maxRows: 4 }}
                        onChange={(event: ChangeEvent<HTMLTextAreaElement>) => setEditingText(event.target.value)}
                        onBlur={() => {
                          const next = editingText.trim();
                          if (next && next !== item.text) onUpdate(item, next);
                          setEditingID("");
                        }}
                        onPressEnter={(event: KeyboardEvent<HTMLTextAreaElement>) => {
                          if (event.shiftKey) return;
                          event.preventDefault();
                          event.currentTarget.blur();
                        }}
                      />
                    ) : (
                      <Tooltip title={item.text} placement="topLeft">
                        <span className="follow-up-text">{item.text}</span>
                      </Tooltip>
                    )}
                    <span className="follow-up-status" role="status">
                      <span className="follow-up-status-icon" aria-hidden="true">{statusIcon(item)}</span>
                      <span>{statusLabel(item)}</span>
                      {item.status === "failed" && item.error_message ? ` · ${item.error_message}` : ""}
                    </span>
                  </div>
                  <div className="follow-up-actions">
                    {(item.status === "pending" && isStreaming) || item.status === "steering" ? (
                      <Button
                        className="follow-up-steer"
                        type="text"
                        size="small"
                        disabled={item.status === "steering"}
                        icon={<EnterOutlined />}
                        aria-label={t("chat.followUpSteer")}
                        onClick={() => onSteer(item)}
                      >
                        {t("chat.followUpSteer")}
                      </Button>
                    ) : null}
                    {item.status === "failed" ? (
                      <Tooltip title={t("chat.followUpRetry")}>
                        <Button aria-label={t("chat.followUpRetry")} type="text" size="small" icon={<RedoOutlined />} onClick={() => onRetry(item)} />
                      </Tooltip>
                    ) : null}
                    {(item.status === "pending" || item.status === "failed") ? (
                      <>
                        <Tooltip title={t("chat.followUpEdit")}>
                          <Button
                            type="text"
                            size="small"
                            aria-label={t("chat.followUpEdit")}
                            icon={<EditOutlined />}
                            onClick={() => { setEditingID(item.id); setEditingText(item.text); }}
                          />
                        </Tooltip>
                        <Tooltip title={t("chat.followUpDelete")}>
                          <Button aria-label={t("chat.followUpDelete")} type="text" size="small" icon={<DeleteOutlined />} onClick={() => onRemove(item)} />
                        </Tooltip>
                      </>
                    ) : null}
                  </div>
                </div>
              )}
            </SortableFollowUp>
          ))}
        </SortableContext>
      </DndContext>
    </section>
  );
}
