import { useState, useEffect } from 'react';
import ReactDOM from 'react-dom';
import FileViewer from '@/modules/knowledge/components/FileViewer';
import { resolveCoreAssetUrl } from '@/modules/knowledge/utils/imageUrl';
import { useTranslation } from 'react-i18next';

interface FilePreviewDrawerProps {
  open: boolean;
  filename: string;
  url: string;
  onClose: () => void;
}

export function FilePreviewDrawer({ open, filename, url, onClose }: FilePreviewDrawerProps) {
  const { t } = useTranslation();
  const [resolvedUrl, setResolvedUrl] = useState<string>('');

  useEffect(() => {
    if (!open || !url) return;
    const sync = resolveCoreAssetUrl(url);
    setResolvedUrl(sync);
  }, [open, url]);

  if (!open) return null;

  return ReactDOM.createPortal(
    <div
      className='file-preview-drawer__overlay'
      onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}
      role='presentation'
    >
      <div className='file-preview-drawer' role='dialog' aria-label={t('chat.previewNamedFile', { name: filename })} aria-modal='true'>
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
          {resolvedUrl ? (
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
