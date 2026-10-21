import { useEffect, useState } from 'react';
import { Alert, Button, List, Pagination } from 'antd';
import { useTranslation } from 'react-i18next';
import { getDirectory, type DocumentDirectoryEntry } from './api';
export default function ExcludedEntries({ directoryId, version, blocked, restore }: { directoryId: string; version: number; blocked?: boolean; restore: (id: string) => void }) {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);
  const [rows, setRows] = useState<DocumentDirectoryEntry[]>([]);
  const [total, setTotal] = useState(0);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let active = true;
    void getDirectory(directoryId, { offset: (page - 1) * 20, limit: 20, state: 'excluded' }).then(result => {
      if (active) {
        setRows(result.entries.filter(e => e.excluded));
        setTotal(result.total || 0);
        setPage(current => Math.min(current, Math.max(1, Math.ceil((result.total || 0) / 20))));
        setFailed(false);
      }
    }).catch(() => { if (active) setFailed(true); });
    return () => { active = false; };
  }, [directoryId, version, page]);
  return <>{failed && <Alert type="error" message={t('directories.sourceTreeFailed')} />}
    <List dataSource={rows} renderItem={(entry: DocumentDirectoryEntry) => <List.Item key={entry.id} actions={[<Button key="restore" disabled={blocked} onClick={() => restore(entry.id)}>{t('directories.restore')}</Button>]}>{entry.title || entry.resource_id}</List.Item>} />
    <Pagination current={page} total={total} pageSize={20} showSizeChanger={false} hideOnSinglePage onChange={setPage} />
  </>;
}
