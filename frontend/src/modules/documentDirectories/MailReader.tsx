import { useEffect, useState } from 'react';
import { Alert, Button, Descriptions, Drawer, Empty, Space, Spin, Typography } from 'antd';
import { useTranslation } from 'react-i18next';
import { getLocalizedErrorMessage } from '@/components/request';
import { readDirectoryMail, type DirectoryMail, type DocumentDirectoryEntry } from './api';

export default function MailReader({ directoryId, entry, onClose }: {
  directoryId?: string; entry?: DocumentDirectoryEntry; onClose: () => void;
}) {
  const { t } = useTranslation();
  const [mail, setMail] = useState<DirectoryMail>();
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const entryId = entry?.id;
  useEffect(() => {
    setMail(undefined); setError(''); setLoading(false);
    if (!entryId || !directoryId) return;
    const controller = new AbortController();
    setLoading(true);
    void readDirectoryMail(directoryId, entryId, controller.signal).then(value => {
      if (!controller.signal.aborted) setMail(value);
    }).catch(err => {
      if (!controller.signal.aborted) setError(getLocalizedErrorMessage(err));
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });
    return () => controller.abort();
  }, [directoryId, entryId, attempt]);
  return <Drawer title={t('directories.viewMail')} open={!!entry} width={720} onClose={onClose}>
    {loading ? <Spin /> : error ? <Space direction="vertical"><Alert type="error" showIcon message={error} /><Button onClick={() => setAttempt(value => value + 1)}>{t('directories.retry')}</Button></Space> : mail && <>
      <Typography.Title level={4}>{mail.subject || entry?.title}</Typography.Title>
      <Descriptions size="small" column={1} className="directory-mail-meta" items={[
        { key: 'from', label: t('directories.mailFrom'), children: mail.from },
        { key: 'to', label: t('directories.mailTo'), children: mail.to },
        ...(mail.cc ? [{ key: 'cc', label: t('directories.mailCC'), children: mail.cc }] : []),
        { key: 'date', label: t('directories.mailDate'), children: mail.date },
        { key: 'mailbox', label: t('directories.mailbox'), children: mail.mailbox },
      ]} />
      {mail.truncated && <Alert type="info" showIcon message={t('directories.mailTruncated')} />}
      {mail.body ? <div className="directory-mail-body">{mail.body}</div> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('directories.mailEmpty')} />}
      {mail.attachments.length > 0 && <><Typography.Title level={5}>{t('directories.mailAttachments')}</Typography.Title>{mail.attachments.map((item, i) => <Typography.Paragraph key={i}>{item.filename}{item.size > 0 ? ` (${item.size} B)` : ''}</Typography.Paragraph>)}</>}
    </>}
  </Drawer>;
}
