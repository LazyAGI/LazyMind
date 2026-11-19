import { useEffect, useRef } from 'react';

interface Props {
  side?: "left" | "right";
  width: number;
  maxWidth: number;
  onChange: (width: number) => void;
  /** Lightweight visual update; undefined ends the preview without persisting it. */
  onPreview?: (width: number | undefined) => void;
  label: string;
}

export function PaperPaneDivider({ width, maxWidth, onChange, onPreview, label, side="right" }: Props) {
  const drag = useRef<{ x: number; width: number; next: number; pointerId: number }>();
  const frame = useRef<number>();
  const preview = useRef(onPreview);
  preview.current = onPreview;
  const direction = side === "left" ? -1 : 1;
  const minimum = Math.min(320, maxWidth);
  const clamp = (next: number) => Math.round(Math.max(minimum, Math.min(maxWidth, next)));
  const clearPreview = () => {
    if (frame.current !== undefined) cancelAnimationFrame(frame.current);
    frame.current = undefined;
    drag.current = undefined;
    preview.current?.(undefined);
  };
  useEffect(() => () => {
    if (frame.current !== undefined) cancelAnimationFrame(frame.current);
    if (drag.current) preview.current?.(undefined);
  }, []);

  return <div
    className="paper-pane-divider"
    role="separator"
    tabIndex={0}
    aria-label={label}
    aria-orientation="vertical"
    aria-valuemin={minimum}
    aria-valuemax={maxWidth}
    aria-valuenow={width}
    onDoubleClick={() => onChange(clamp(400))}
    onKeyDown={event => {
      const next = event.key === 'ArrowLeft' ? width + 24 * direction
        : event.key === 'ArrowRight' ? width - 24 * direction
        : event.key === 'Home' ? minimum
        : event.key === 'End' ? maxWidth : undefined;
      if (next !== undefined) { event.preventDefault(); onChange(clamp(next)); }
    }}
    onPointerDown={event => {
      if (event.button !== 0 || drag.current) return;
      drag.current = {x:event.clientX, width, next:width, pointerId:event.pointerId};
      event.currentTarget.setPointerCapture(event.pointerId);
      event.preventDefault();
    }}
    onPointerMove={event => {
      const current = drag.current;
      if (!current || current.pointerId !== event.pointerId) return;
      current.next = clamp(current.width + (current.x - event.clientX) * direction);
      if (frame.current !== undefined) return;
      frame.current = requestAnimationFrame(() => {
        frame.current = undefined;
        if (drag.current) preview.current?.(drag.current.next);
      });
    }}
    onPointerUp={event => {
      const current = drag.current;
      if (!current || current.pointerId !== event.pointerId) return;
      const next = clamp(current.width + (current.x - event.clientX) * direction);
      clearPreview();
      if (next !== width) onChange(next);
    }}
    onPointerCancel={event => { if (drag.current?.pointerId === event.pointerId) clearPreview(); }}
    onLostPointerCapture={event => { if (drag.current?.pointerId === event.pointerId) clearPreview(); }}
  />;
}
