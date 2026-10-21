import { useState, useEffect, useRef } from 'react';
import ReactDOM from 'react-dom';
import FileViewer from '@/modules/knowledge/components/FileViewer';
import { resolveCoreAssetUrl } from '@/modules/knowledge/utils/imageUrl';
import { useTranslation } from 'react-i18next';
import MarkdownViewer from '../MarkdownViewer';
import { SourceImportProvider, ExecutionResults } from '../SourceImports';
import { ReferenceSourcesButton } from '../SourceImports/ReferenceSources';
import type { SourceContext } from '../SourceImports/api';
import type { ChatSource } from '../../utils/sourceAdapter';

interface FilePreviewDrawerProps {
  open: boolean;
  filename: string;
  url: string;
  onClose: () => void;
  content?: string;
  sourceContext?: SourceContext;
  sources?: ChatSource[];
}

export function FilePreviewDrawer({ open, filename, url, onClose, content, sourceContext, sources }: FilePreviewDrawerProps) {
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
      // Nested citation popovers and the target modal own their focus and Escape.
      if (!dialog?.contains(event.target as Node)) return;
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
  const renderMarkdown = sourceContext ? (markdown: string) => <MarkdownViewer sources={sources}>{markdown}</MarkdownViewer> : undefined;
  const body = <>
    {content !== undefined ? renderMarkdown ? renderMarkdown(content) : <pre className="ordinary-inline-preview">{content}</pre> : resolvedUrl ? (
      <FileViewer file={resolvedUrl} fileName={filename} renderMarkdown={renderMarkdown} />
    ) : <div className='file-preview-drawer__loading'>{t('common.loading')}</div>}
    {sourceContext && <div className="chat-assistant-msg-tool-actions"><ReferenceSourcesButton sources={sources} /><ExecutionResults /></div>}
  </>;

  return ReactDOM.createPortal(
    <div
      className='file-preview-drawer__overlay'
      style={sourceContext ? { zIndex: 900 } : undefined}
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
          {sourceContext ? <SourceImportProvider context={sourceContext} sources={sources}>{body}</SourceImportProvider> : body}
        </div>
      </div>
    </div>,
    document.body,
  );
}
