import { cloneElement, useEffect, useRef, useState, type HTMLAttributes, type ReactElement } from "react";
import { Popover, type TooltipRef } from "antd";
import dayjs from "dayjs";
import { useTranslation } from "react-i18next";
import { useConversationRunningStore } from "@/modules/chat/store/conversationRunning";
import "./ConversationPreview.scss";

interface ConversationPreviewProps {
  conversationId: string;
  title: string;
  summary?: string;
  updateTime?: string;
  isTask?: boolean;
  relation?: string;
  disabled?: boolean;
  children: ReactElement<HTMLAttributes<HTMLElement>>;
}

export default function ConversationPreview({
  conversationId, title, summary, updateTime, isTask, relation, disabled, children,
}: ConversationPreviewProps) {
  const { t } = useTranslation();
  const ref = useRef<TooltipRef>(null);
  const [open, setOpen] = useState(false);
  const [layout, setLayout] = useState({ offset: 8, width: 300 });
  const entry = useConversationRunningStore(state => state.entries[conversationId]);
  const status = entry?.status === "idle" ? entry.terminalStatus : entry?.status;
  const statusLabels = {
    running: "chat.conversationRunning",
    unknown: "chat.conversationStatusUnavailable",
    completed: "chat.conversationCompleted",
    failed: "chat.conversationFailed",
    canceled: "chat.conversationCanceled",
  };
  const updated = updateTime ? dayjs(updateTime) : null;

  useEffect(() => {
    if (!open) return;
    const close = () => setOpen(false);
    const onKeyDown = (event: KeyboardEvent) => { if (event.key === "Escape") close(); };
    window.addEventListener("resize", close);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("resize", close);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open]);

  const changeOpen = (next: boolean) => {
    const trigger = ref.current?.nativeElement;
    if (!next || disabled || !trigger) { setOpen(false); return; }
    const row = trigger.getBoundingClientRect();
    const edge = trigger.closest(".ant-layout-sider")?.getBoundingClientRect().right ?? row.right;
    const width = Math.min(300, window.innerWidth - edge - 20);
    if (width < 160 || trigger.closest(".record-sortable--dragging")) return;
    setLayout({ offset: edge - row.right + 8, width });
    setOpen(true);
  };

  return <Popover
    ref={ref}
    open={open && !disabled}
    onOpenChange={changeOpen}
    trigger={["hover", "focus"]}
    placement="rightTop"
    align={{ offset: [layout.offset, 0] }}
    autoAdjustOverflow={{ adjustX: 0, adjustY: 0, shiftY: 12 }}
    arrow={false}
    mouseEnterDelay={0.25}
    mouseLeaveDelay={0.12}
    destroyOnHidden
    classNames={{ root: "record-preview-popover" }}
    styles={{ root: { width: layout.width } }}
    content={<section className="record-preview-card" aria-label={t("chat.conversationPreview")} onClick={event => event.stopPropagation()}>
      <strong className="record-preview-title">{title}</strong>
      {summary?.trim() && <p className="record-preview-summary">{summary.trim()}</p>}
      <div className="record-preview-meta">
        {isTask !== undefined && <span>{t(isTask ? "chat.conversationPreviewWork" : "chat.conversationPreviewChat")}</span>}
        {status && <span>{t(statusLabels[status])}</span>}
      </div>
      {relation && <div className="record-preview-relation">{relation}</div>}
      {updated?.isValid() && <time className="record-preview-time" dateTime={updateTime}>{t("chat.conversationPreviewUpdated", { time: updated.format("YYYY/MM/DD HH:mm") })}</time>}
    </section>}
  >{cloneElement(children, {
    onClickCapture: event => { setOpen(false); children.props.onClickCapture?.(event); },
    onDragStartCapture: event => { setOpen(false); children.props.onDragStartCapture?.(event); },
  })}</Popover>;
}
