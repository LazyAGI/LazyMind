import { useRef } from 'react';

interface Props {
  side?: "left" | "right";
  width: number;
  maxWidth: number;
  onChange: (width: number) => void;
  label: string;
}

/** The assistant sits to the right, so dragging left increases its width. */
export function PaperPaneDivider({ width, maxWidth, onChange, label, side="right" }: Props) {
  const drag = useRef<{ x: number; width: number }>();
  const direction = side === "left" ? -1 : 1;
  const minimum = Math.min(320, maxWidth);
  const resize = (next: number) => onChange(Math.round(Math.max(minimum, Math.min(maxWidth, next))));
  return <div
    className="paper-pane-divider"
    role="separator"
    tabIndex={0}
    aria-label={label}
    aria-orientation="vertical"
    aria-valuemin={minimum}
    aria-valuemax={maxWidth}
    aria-valuenow={width}
    onDoubleClick={() => resize(400)}
    onKeyDown={event => {
      const next = event.key === 'ArrowLeft' ? width + 24 * direction
        : event.key === 'ArrowRight' ? width - 24 * direction
        : event.key === 'Home' ? minimum
        : event.key === 'End' ? maxWidth : undefined;
      if (next !== undefined) { event.preventDefault(); resize(next); }
    }}
    onPointerDown={event => {
      if (event.button !== 0) return;
      drag.current = {x:event.clientX, width};
      event.currentTarget.setPointerCapture(event.pointerId);
      event.preventDefault();
    }}
    onPointerMove={event => {
      if (drag.current) resize(drag.current.width + (drag.current.x - event.clientX) * direction);
    }}
    onPointerUp={() => { drag.current = undefined; }}
    onPointerCancel={() => { drag.current = undefined; }}
    onLostPointerCapture={() => { drag.current = undefined; }}
  />;
}
