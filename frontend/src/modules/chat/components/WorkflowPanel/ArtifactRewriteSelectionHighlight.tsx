import { useLayoutEffect, useRef, useState } from 'react';
import ReactDOM from 'react-dom';
import './ArtifactRewriteSelectionHighlight.scss';

interface HighlightBox {
  top: number;
  left: number;
  width: number;
  height: number;
}

interface ArtifactRewriteSelectionHighlightProps {
  layer: HTMLElement | null;
  getRange: () => Range | null;
  active: boolean;
  observeRoot?: HTMLElement | null;
}

export function ArtifactRewriteSelectionHighlight({
  layer,
  getRange,
  active,
  observeRoot,
}: ArtifactRewriteSelectionHighlightProps) {
  const [boxes, setBoxes] = useState<HighlightBox[]>([]);
  const getRangeRef = useRef(getRange);
  getRangeRef.current = getRange;

  useLayoutEffect(() => {
    if (!active || !layer) {
      setBoxes([]);
      return undefined;
    }

    const update = () => {
      const range = getRangeRef.current();
      if (!range) {
        setBoxes([]);
        return;
      }
      const layerRect = layer.getBoundingClientRect();
      setBoxes(
        Array.from(range.getClientRects())
          .filter((rect) => rect.width > 0 || rect.height > 0)
          .map((rect) => ({
            top: rect.top - layerRect.top,
            left: rect.left - layerRect.left,
            width: rect.width,
            height: rect.height,
          })),
      );
    };

    update();
    const resizeObserver = typeof ResizeObserver !== 'undefined'
      ? new ResizeObserver(update)
      : null;
    resizeObserver?.observe(layer);
    if (observeRoot) resizeObserver?.observe(observeRoot);
    window.addEventListener('resize', update);
    window.addEventListener('scroll', update, true);
    return () => {
      resizeObserver?.disconnect();
      window.removeEventListener('resize', update);
      window.removeEventListener('scroll', update, true);
    };
  }, [active, layer, observeRoot]);

  if (!layer || !active || boxes.length === 0) return null;

  return ReactDOM.createPortal(
    <>
      {boxes.map((box, index) => (
        <div
          key={index}
          className='artifact-rewrite-selection-highlight'
          style={{
            top: box.top,
            left: box.left,
            width: box.width,
            height: box.height,
          }}
        />
      ))}
    </>,
    layer,
  );
}
