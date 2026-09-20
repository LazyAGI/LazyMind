import { useTranslation } from 'react-i18next';
import { useEffect, useId, useRef, type ReactNode } from 'react';

export function WriterDocumentOptions({ sourceMode, onSourceMode, width, onWidth, children }: {
  sourceMode?: boolean; onSourceMode?: () => void;
  width: string; onWidth: (width: 'default' | 'wide' | 'reading') => void; children?: ReactNode;
}) {
  const { t } = useTranslation();
  const name = useId();
  const menu = useRef<HTMLDetailsElement>(null);
  useEffect(() => {
    const ownerDocument = menu.current?.ownerDocument;
    if (!ownerDocument) return;
    let pointerInside = false;
    const closeOutside = (event: Event) => {
      const details = menu.current;
      if (details && event.target instanceof Node && !details.contains(event.target)) details.open = false;
    };
    const handlePointerDown = (event: PointerEvent) => {
      pointerInside = event.target instanceof Node && Boolean(menu.current?.contains(event.target));
      closeOutside(event);
    };
    const handlePointerEnd = () => { pointerInside = false; };
    const handleFocus = (event: FocusEvent) => {
      // Label presses can briefly focus a parent content item before the radio.
      // Keep native details mounted until that pointer interaction completes.
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
  const close = () => { if (menu.current) { menu.current.open = false; menu.current.querySelector('summary')?.focus(); } };
  return <details className='writer-document-options' ref={menu}
    onKeyDown={event => { if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); close(); } }}>
    <summary aria-label={t('chat.writerMarkdown.moreActions')}>{t('chat.writerMarkdown.moreActions')}</summary>
    <div className='writer-document-options__menu'>
      {onSourceMode && <button type='button' onClick={() => { onSourceMode(); close(); }}>{t(sourceMode ? 'chat.writerLocal.backToDocument' : 'chat.writerSource.source')}</button>}
      <fieldset><legend>{t('chat.writerIR.pageWidth')}</legend>
        {(['default', 'wide', 'reading'] as const).map(value => <label key={value}>
          <input type='radio' name={name} checked={width === value} onChange={() => onWidth(value)} />
          {t(value === 'reading' ? 'chat.writerSource.readingWidth' : `chat.writerIR.pageWidths.${value}`)}
        </label>)}
      </fieldset>
      {children}
    </div>
  </details>;
}
