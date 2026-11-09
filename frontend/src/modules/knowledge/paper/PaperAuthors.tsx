import { CloseOutlined, DownOutlined } from '@ant-design/icons';
import { Button, Popover } from 'antd';
import { useId, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import './PaperAuthors.scss';

const MIN_PREVIEW_AUTHORS = 3;
const AUTHOR_SEPARATOR = ', ';
const formatCount = new Intl.NumberFormat('zh-CN');
const moreAuthorsLabel = (remaining: number) => `+${formatCount.format(remaining)} 位`;

export default function PaperAuthors({ authors, empty = '作者未记录', fitToWidth = false }: {
  authors: readonly string[];
  empty?: string;
  fitToWidth?: boolean;
}) {
  const names = useMemo(() => authors.map(name => name.trim()).filter(Boolean), [authors]);
  const [fitCount, setFitCount] = useState(MIN_PREVIEW_AUTHORS);
  const preview = names.slice(0, fitToWidth ? fitCount : MIN_PREVIEW_AUTHORS);
  const remaining = names.length - preview.length;
  const total = formatCount.format(names.length);
  const panelId = useId();
  const triggerRef = useRef<HTMLButtonElement>(null);
  const rowRef = useRef<HTMLSpanElement>(null);
  const namesProbeRef = useRef<HTMLSpanElement>(null);
  const badgeProbeRef = useRef<HTMLButtonElement>(null);
  const badgeTextRef = useRef<HTMLSpanElement>(null);
  const [open, setOpen] = useState(false);

  useLayoutEffect(() => {
    if (!fitToWidth) return;
    if (names.length <= MIN_PREVIEW_AUTHORS) {
      setFitCount(MIN_PREVIEW_AUTHORS);
      setOpen(false);
      return;
    }

    const row = rowRef.current;
    const namesProbe = namesProbeRef.current;
    const badgeProbe = badgeProbeRef.current;
    const badgeText = badgeTextRef.current;
    const text = namesProbe?.firstChild;
    if (!row || !namesProbe || !text || !badgeProbe || !badgeText) return;

    const range = row.ownerDocument.createRange();
    const measure = () => {
      const width = row.getBoundingClientRect().width;
      if (width === 0) return;
      if (namesProbe.getBoundingClientRect().width <= width) {
        setFitCount(names.length);
        setOpen(false);
        return;
      }

      const gap = Number.parseFloat(getComputedStyle(row).columnGap);
      let count = MIN_PREVIEW_AUTHORS;
      let end = 0;
      let badgeWidth = 0;
      let badgeLength = 0;
      range.setStart(text, 0);
      for (let index = 0; index < names.length - 1; index++) {
        end += names[index].length + (index === 0 ? 0 : AUTHOR_SEPARATOR.length);
        if (index + 1 <= MIN_PREVIEW_AUTHORS) continue;
        range.setEnd(text, end);
        const textWidth = range.getBoundingClientRect().width;
        if (textWidth >= width) break;
        const label = moreAuthorsLabel(names.length - index - 1);
        // The button uses tabular digits, so counts of the same length share a width.
        if (label.length !== badgeLength) {
          badgeText.textContent = label;
          badgeWidth = badgeProbe.getBoundingClientRect().width;
          badgeLength = label.length;
        }
        if (textWidth + gap + badgeWidth <= width) count = index + 1;
      }
      setFitCount(count);
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(row);
    observer.observe(namesProbe);
    return () => observer.disconnect();
  }, [fitToWidth, names]);

  const close = () => {
    setOpen(false);
    triggerRef.current?.focus({ preventScroll: true });
  };
  const closeOnEscape = (event: KeyboardEvent<HTMLElement>) => {
    if (!open || event.key !== 'Escape') return;
    event.preventDefault();
    event.stopPropagation();
    close();
  };

  return <span ref={rowRef} className={fitToWidth ? 'paper-authors paper-authors-adaptive' : 'paper-authors'}>
    <span className="paper-authors-leading">{preview.join(AUTHOR_SEPARATOR) || empty}</span>
    {remaining > 0 && <Popover
      trigger="click"
      placement="bottomLeft"
      align={{ overflow: { adjustX: false, shiftX: true, adjustY: true, shiftY: true } }}
      arrow={false}
      open={open}
      onOpenChange={setOpen}
      destroyOnHidden
      styles={{ body: { padding: 0 } }}
      content={<div id={panelId} className="paper-authors-panel" role="dialog" aria-label="全部作者" onKeyDown={closeOnEscape}>
        <div className="paper-authors-panel-head">
          <strong>全部作者</strong>
          <span>共 {total} 位</span>
          <Button type="text" size="small" autoFocus icon={<CloseOutlined />} aria-label="关闭作者名单" onClick={close} />
        </div>
        <ol className="paper-authors-list" aria-label="作者名单" tabIndex={0}>
          {open && names.map((name, index) => <li key={index}>{name}</li>)}
        </ol>
      </div>}
    >
      <Button
        ref={triggerRef}
        className="paper-authors-more"
        type="text"
        size="small"
        aria-label={`查看全部 ${total} 位作者`}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={open ? panelId : undefined}
        onKeyDown={closeOnEscape}
      >
        {moreAuthorsLabel(remaining)} <DownOutlined aria-hidden />
      </Button>
    </Popover>}
    {fitToWidth && names.length > MIN_PREVIEW_AUTHORS && <span className="paper-authors-probes" aria-hidden="true">
      <span ref={namesProbeRef} className="paper-authors-names-probe">{names.join(AUTHOR_SEPARATOR)}</span>
      <Button ref={badgeProbeRef} className="paper-authors-more" type="text" size="small" tabIndex={-1}>
        <span ref={badgeTextRef}>{moreAuthorsLabel(names.length - MIN_PREVIEW_AUTHORS)}</span> <DownOutlined aria-hidden />
      </Button>
    </span>}
  </span>;
}
