import { useEffect, useState } from 'react';
import { Alert, Breadcrumb, Button, Empty, List, Pagination, Spin, Tag } from 'antd';
import { useTranslation } from 'react-i18next';
import { getSourceNodes, type DocumentFolderNode } from './api';

export default function SourceTree({ directoryId }: { directoryId: string }) {
  const { t } = useTranslation();
  const [parents, setParents] = useState<{ key: string; name: string }[]>([]);
  const [page, setPage] = useState(1);
  const [nodes, setNodes] = useState<DocumentFolderNode[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const parent = parents[parents.length - 1]?.key || '';
  useEffect(() => {
    let disposed = false; setLoading(true); setFailed(false);
    void getSourceNodes(directoryId, parent, (page - 1) * 100).then(result => {
      if (!disposed) { setNodes(result.nodes); setTotal(result.total); }
    }).catch(() => { if (!disposed) setFailed(true); }).finally(() => { if (!disposed) setLoading(false); });
    return () => { disposed = true; };
  }, [directoryId, parent, page]);
  return <section>
    <Alert type="info" message={t('directories.sourceSnapshot')} />
    <Breadcrumb items={[{ title: <Button type="link" onClick={() => { setParents([]); setPage(1); }}>{t('directories.sourceTree')}</Button> }, ...parents.map((item, index) => ({ title: <Button type="link" onClick={() => { setParents(parents.slice(0, index + 1)); setPage(1); }}>{item.name}</Button> }))]} />
    {loading ? <Spin /> : failed ? <Alert type="error" message={t('directories.sourceTreeFailed')} /> : <List dataSource={nodes} locale={{ emptyText: <Empty description={t('directories.emptySourceFolder')} /> }} renderItem={(item: DocumentFolderNode) => <List.Item>
      <div>{item.kind === 'directory' ? <Button type="link" onClick={() => { setParents([...parents, { key: item.node_key, name: item.name }]); setPage(1); }}>{item.name}</Button> : <span>{item.name}</span>}
        {item.kind === 'link' && <Tag>{t('directories.sourceLink')}</Tag>}
        <div>{item.path || '/'}</div>
      </div>
    </List.Item>} />}
    <Pagination current={page} total={total} pageSize={100} showSizeChanger={false} onChange={setPage} hideOnSinglePage />
  </section>;
}
