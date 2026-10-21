import { useEffect, useState } from 'react';
import { Alert, Button, Modal, Space, Spin, message } from 'antd';
import { useTranslation } from 'react-i18next';
import FileViewer from '@/modules/knowledge/components/FileViewer';
import { getLocalizedErrorMessage } from '@/components/request';
import { hasDesktopFileBridge, showItemInFolder } from '@/runtime/desktopBridge';
import { readDirectoryLocalFile, type DocumentDirectoryEntry } from './api';

export default function LocalFileReader({ directoryId, entry, onClose }: {
  directoryId?: string; entry?: DocumentDirectoryEntry; onClose: () => void;
}) {
  const { t } = useTranslation();
  const [url, setUrl] = useState('');
  const [filename, setFilename] = useState('');
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  const [revealing, setRevealing] = useState(false);
  const entryId = entry?.id;
  useEffect(() => {
    setUrl(''); setError(''); setFilename('');
    if (!entryId || !directoryId) return;
    const controller = new AbortController();
    let objectUrl = '';
    void readDirectoryLocalFile(directoryId, entryId, controller.signal).then(file => {
      if (controller.signal.aborted) return;
      if (file.error_code) { setError(t(`directories.localFile_${file.error_code}`, { defaultValue: t('directories.localFile_unavailable') })); return; }
      const bytes = Uint8Array.from(atob(file.content_base64), char => char.charCodeAt(0));
      objectUrl = URL.createObjectURL(new Blob([bytes], { type: file.mime_type }));
      setFilename(file.filename); setUrl(objectUrl);
    }).catch(err => { if (!controller.signal.aborted) setError(getLocalizedErrorMessage(err)); });
    return () => { controller.abort(); if (objectUrl) URL.revokeObjectURL(objectUrl); };
  }, [directoryId, entryId, attempt, t]);
  const reveal = async () => {
    if (!directoryId || !entryId) return;
    setRevealing(true);
    try {
      const result = await showItemInFolder({ source: '', directoryEntry: { directoryId, entryId } });
      if (!result.ok) message.error(t('directories.localFileRevealFailed'));
    } finally { setRevealing(false); }
  };
  return <Modal open={!!entry} centered width="90vw" footer={null} onCancel={onClose}
    style={{ maxWidth: 1400, paddingBottom: 0 }}
    styles={{ content: { height: '85vh', display: 'flex', flexDirection: 'column' }, header: { flexShrink: 0 }, body: { flex: 1, minHeight: 0, overflow: 'auto' } }}
    title={<div style={{ display: 'flex', alignItems: 'center', gap: 16, paddingRight: 32 }}>
      <span style={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={entry?.title || filename}>{entry?.title || filename || t('directories.previewFile')}</span>
      {hasDesktopFileBridge() && <Button loading={revealing} onClick={() => void reveal()}>{t('directories.showInFolder')}</Button>}
    </div>}>
    {error ? <Space direction="vertical"><Alert type="error" showIcon message={error} /><Button onClick={() => setAttempt(v => v + 1)}>{t('directories.retry')}</Button></Space>
      : url ? <FileViewer file={url} fileName={filename} /> : <Spin />}
  </Modal>;
}
