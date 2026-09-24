import { useState, useEffect, useRef } from 'react';
import ReactDOM from 'react-dom';
import FileViewer from '@/modules/knowledge/components/FileViewer';
import { resolveCoreAssetUrl } from '@/modules/knowledge/utils/imageUrl';
import { useTranslation } from 'react-i18next';

interface FilePreviewDrawerProps {
  open: boolean;
  filename: string;
  url: string;
  onClose: () => void;
  content?: string;
}

export function FilePreviewDrawer({ open, filename, url, onClose, content }: FilePreviewDrawerProps) {
  const { t } = useTranslation();
  const dialogRef = useRef<HTMLDivElement>(null);
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  const [resolvedUrl, setResolvedUrl] = useState<string>('');

  useEffect(() => {
    if (!open || !url) { setResolvedUrl(""); return; }
    const sync = resolveCoreAssetUrl(url);
    setResolvedUrl(sync);
  }, [open, url]);

  useEffect(() => {
    if (!open) return;
    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const dialog = dialogRef.current;
    dialog?.querySelector<HTMLButtonElement>("button")?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); closeRef.current(); }
      if (event.key !== "Tab" || !dialog) return;
      const focusable = [...dialog.querySelectorAll<HTMLElement>('button:not(:disabled), a[href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex="0"]')];
      const first = focusable[0], last = focusable[focusable.length - 1];
      if (!first) { event.preventDefault(); dialog.focus(); }
      else if (event.shiftKey && (document.activeElement === first || !dialog.contains(document.activeElement))) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && (document.activeElement === last || !dialog.contains(document.activeElement))) { event.preventDefault(); first.focus(); }
    };
    document.addEventListener("keydown", onKeyDown, true);
    return () => { document.removeEventListener("keydown", onKeyDown, true); previousFocus?.focus(); };
  }, [open]);

  if (!open) return null;

  return ReactDOM.createPortal(
    <div
      className='file-preview-drawer__overlay'
      onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}
      role='presentation'
    >
      <div className='file-preview-drawer' ref={dialogRef} tabIndex={-1} role='dialog' aria-label={t('chat.previewNamedFile', { name: filename })} aria-modal='true'>
        <div className='file-preview-drawer__header'>
          <span className='file-preview-drawer__title'>{filename}</span>
          <button
            className='file-preview-drawer__close'
            onClick={onClose}
            aria-label={t('chat.closePreview')}
            type='button'
          >×</button>
        </div>
        <div className='file-preview-drawer__body'>
          {content !== undefined ? <pre className="ordinary-inline-preview">{content}</pre> : resolvedUrl ? (
            <FileViewer file={resolvedUrl} fileName={filename} />
          ) : (
            <div className='file-preview-drawer__loading'>{t('common.loading')}</div>
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
}
