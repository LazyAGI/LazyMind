import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

export const SIDEBAR_MIN_WIDTH = 72;
export const SIDEBAR_MAX_WIDTH = 272;
const SIDEBAR_EXPANDED_MIN_WIDTH = 200;

export function normalizeSidebarWidth(width: number) {
  return width < SIDEBAR_EXPANDED_MIN_WIDTH
    ? SIDEBAR_MIN_WIDTH
    : Math.min(SIDEBAR_MAX_WIDTH, width);
}

export default function SidebarResizeHandle({
  width,
  onResize,
}: {
  width: number;
  onResize: (width: number) => void;
}) {
  const { t } = useTranslation();
  const drag = useRef<{ pointerId: number; x: number; width: number } | null>(null);
  const [dragging, setDragging] = useState(false);
  const [snapTarget, setSnapTarget] = useState<number | null>(null);
  const stopDragging = () => {
    drag.current = null;
    setDragging(false);
  };
  const resize = (value: number) => {
    onResize(normalizeSidebarWidth(value));
  };

  useEffect(() => {
    if (snapTarget === null) return;
    // Keep the snap smooth before resuming direct pointer tracking.
    const timeout = window.setTimeout(() => setSnapTarget(null), 180);
    return () => window.clearTimeout(timeout);
  }, [snapTarget]);

  useEffect(() => {
    if (!dragging) return;
    const { cursor, userSelect } = document.body.style;
    document.body.style.cursor = "col-resize";
    document.body.style.userSelect = "none";
    window.addEventListener("blur", stopDragging);
    return () => {
      document.body.style.cursor = cursor;
      document.body.style.userSelect = userSelect;
      window.removeEventListener("blur", stopDragging);
    };
  }, [dragging]);

  return (
    <div
      role="separator"
      tabIndex={0}
      aria-label={t("layout.resizeSidebar")}
      aria-orientation="vertical"
      aria-controls="main-navigation"
      aria-valuemin={SIDEBAR_MIN_WIDTH}
      aria-valuemax={SIDEBAR_MAX_WIDTH}
      aria-valuenow={width}
      className={`sider-resize-handle${dragging ? " is-dragging" : ""}${snapTarget !== null ? " is-snapping" : ""}`}
      onPointerDown={(event) => {
        if (event.button !== 0) return;
        event.preventDefault();
        event.currentTarget.focus();
        event.currentTarget.setPointerCapture(event.pointerId);
        drag.current = { pointerId: event.pointerId, x: event.clientX, width };
        setSnapTarget(null);
        setDragging(true);
      }}
      onPointerMove={(event) => {
        if (!drag.current || drag.current.pointerId !== event.pointerId) return;
        const nextWidth = normalizeSidebarWidth(drag.current.width + event.clientX - drag.current.x);
        if ((nextWidth === SIDEBAR_MIN_WIDTH) !== (width === SIDEBAR_MIN_WIDTH)) {
          setSnapTarget(nextWidth);
        }
        onResize(nextWidth);
      }}
      onPointerUp={stopDragging}
      onPointerCancel={stopDragging}
      onLostPointerCapture={stopDragging}
      onKeyDown={(event) => {
        const widths: Record<string, number> = {
          ArrowLeft: width - 16,
          ArrowRight: width === SIDEBAR_MIN_WIDTH ? SIDEBAR_EXPANDED_MIN_WIDTH : width + 16,
          Home: SIDEBAR_MIN_WIDTH,
          End: SIDEBAR_MAX_WIDTH,
        };
        const nextWidth = widths[event.key];
        if (nextWidth === undefined) return;
        event.preventDefault();
        resize(nextWidth);
      }}
    />
  );
}
