import { useTranslation } from 'react-i18next';
import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { Popover } from 'antd';

export function WriterDocumentOptions({ sourceMode, onSourceMode, width, onWidth, children }: {
  sourceMode?: boolean; onSourceMode?: () => void;
  width: string; onWidth: (width: 'default' | 'wide' | 'reading') => void; children?: ReactNode;
}) {
  const { t } = useTranslation();
  const name = useId();
  const menu = useRef<HTMLDetailsElement>(null);
  const popup = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  useEffect(() => {
    const ownerDocument = menu.current?.ownerDocument;
    if (!ownerDocument) return;
    let pointerInside = false;
    const closeOutside = (event: Event) => {
      const details = menu.current;
      if (details && event.target instanceof Node && !details.contains(event.target) && !popup.current?.contains(event.target)) {
        details.open = false;
        setOpen(false);
      }
    };
    const handlePointerDown = (event: PointerEvent) => {
      pointerInside = event.target instanceof Node && Boolean(menu.current?.contains(event.target) || popup.current?.contains(event.target));
      closeOutside(event);
    };
    const handlePointerEnd = () => { pointerInside = false; };
    const handleFocus = (event: FocusEvent) => {
      // Label presses can briefly focus a parent content item before the radio.
      // Keep the popup mounted until that pointer interaction completes.
      if (!pointerInside) closeOutside(event);
    };
    ownerDocument.addEventListener('focusin', handleFocus);
    ownerDocument.addEventListener('pointerdown', handlePointerDown);
    ownerDocument.addEventListener('pointerup', handlePointerEnd);
    ownerDocument.addEventListener('pointercancel', handlePointerEnd);
    return () => {
      ownerDocument.removeEventListener('focusin', handleFocus);
      ownerDocument.removeEventListener('pointerdown', handlePointerDown);
      ownerDocument.removeEventListener('pointerup', handlePointerEnd);
      ownerDocument.removeEventListener('pointercancel', handlePointerEnd);
    };
  }, []);
  const close = () => { if (menu.current) { menu.current.open = false; setOpen(false); menu.current.querySelector('summary')?.focus(); } };
  const handleKeyDown = (event: KeyboardEvent) => {
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); close(); }
  };
  return <Popover open={open} trigger={[]} placement='bottomRight' autoAdjustOverflow
    getPopupContainer={(trigger: HTMLElement) => trigger.ownerDocument.body}
    content={<div className='writer-document-options__menu' ref={popup} onKeyDown={handleKeyDown}>
      {onSourceMode && <button type='button' onClick={() => { onSourceMode(); close(); }}>{t(sourceMode ? 'chat.writerLocal.backToDocument' : 'chat.writerSource.source')}</button>}
      <fieldset><legend>{t('chat.writerIR.pageWidth')}</legend>
        {(['default', 'wide', 'reading'] as const).map(value => <label key={value}>
          <input type='radio' name={name} checked={width === value} onChange={() => onWidth(value)} />
          {t(value === 'reading' ? 'chat.writerSource.readingWidth' : `chat.writerIR.pageWidths.${value}`)}
        </label>)}
      </fieldset>
      {children}
    </div>}>
    <details className='writer-document-options' ref={menu}
      onToggle={event => setOpen(event.currentTarget.open)} onKeyDown={handleKeyDown}>
      <summary aria-expanded={open} aria-label={t('chat.writerMarkdown.moreActions')}>{t('chat.writerMarkdown.moreActions')}</summary>
    </details>
  </Popover>;
}
