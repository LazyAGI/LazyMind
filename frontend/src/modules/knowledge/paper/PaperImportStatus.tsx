import { useState } from 'react';
import { Alert, Button, Collapse, List, Space, Tag, Typography } from 'antd';
import { isAcademicImportActive as active, type AcademicImportQueueItem } from '../api/academicReferences';

function conciseError(error = '') {
  if (/\b429\b/.test(error)) return 'arXiv 请求受限（429），请稍后重试';
  if (/\b503\b/.test(error)) return 'arXiv 服务暂不可用（503），请稍后重试';
  if (/deadline exceeded|timed?\s*out|timeout/i.test(error)) return 'arXiv 请求超时，请稍后重试';
  return error.length > 140 ? `${error.slice(0, 140)}…` : error || '导入未完成，请查看详情';
}

interface Props {
  items: AcademicImportQueueItem[];
  canWrite: boolean;
  onRetry: (batch: string) => Promise<void>;
}

export default function PaperImportStatus({ items, canWrite, onRetry }: Props) {
  const [retrying, setRetrying] = useState<string>();
  const [error, setError] = useState('');
  const groups = new Map<string, AcademicImportQueueItem[]>();
  [...items].sort((a, b) => b.created_at.localeCompare(a.created_at) || b.item_id.localeCompare(a.item_id)).forEach(item => {
    const key = JSON.stringify([item.target_dataset_id, item.work_id, item.source_provider, item.source_version]);
    groups.set(key, [...groups.get(key) || [], item]);
  });
  const rows = [...groups.values()].map(history => {
    const running = history.find(active);
    const latest = running || history[0];
    const document = history.find(item => item.current_document)?.current_document;
    const failed = !running && (document ? document.processing_status === 'failed' : latest.status === 'failed');
    return { history, latest, document, running: !!running, failed };
  });
  const batches = new Map<string, AcademicImportQueueItem[]>();
  rows.filter(row => row.failed && !row.document).forEach(({ latest }) => {
    if (!batches.has(latest.batch_id)) batches.set(latest.batch_id, items.filter(item => item.batch_id === latest.batch_id && item.status === 'failed'));
  });
  const retry = async (batch: string) => {
    if (retrying) return;
    setRetrying(batch); setError('');
    try { await onRetry(batch); }
    catch (cause) { setError(cause instanceof Error ? cause.message : '重试提交失败，请稍后重试'); }
    finally { setRetrying(undefined); }
  };
  if (!items.length) return null;
  return <Collapse className="paper-library-import-status" items={[{
    key: 'imports',
    label: `论文导入：${rows.filter(row => row.failed).length} 篇待处理 · ${rows.filter(row => row.running).length} 篇进行中`,
    children: <>
      {error && <Alert type="error" showIcon message="重试未提交" description={error} />}
      <List dataSource={rows} renderItem={({ latest, history, document, running, failed }) => <List.Item>
        <div style={{ width: '100%', minWidth: 0 }}>
          <Space wrap><Typography.Text strong>{latest.title}</Typography.Text>{latest.source_version && <Tag>{latest.source_version}</Tag>}
            <Tag color={running ? 'processing' : failed ? 'error' : 'success'}>{running ? '导入中' : document ? document.processing_status === 'failed' ? 'PDF 已保存，处理失败' : document.processing_status === 'ready' ? '已入库' : 'PDF 已保存，处理中' : failed ? '导入失败' : latest.status === 'canceled' ? '已取消' : '导入已完成'}</Tag>
          </Space>
          {failed && <p>{conciseError(document?.processing_error || latest.error_message)}</p>}
          {document && <a href={`/lib/knowledge/knowledge/${encodeURIComponent(latest.target_dataset_id)}/${encodeURIComponent(document.document_id)}`}>查看文档</a>}
          <Collapse ghost size="small" items={[{ key: 'history', label: `历史记录（${history.length} 次）`, children: <List size="small" dataSource={history} renderItem={item => <List.Item>
            <div style={{ overflowWrap: 'anywhere' }}>
              <Typography.Text type="secondary">{new Date(item.created_at).toLocaleString()} · {item.status === 'failed' ? '失败' : item.status === 'succeeded' || item.status === 'skipped' ? '完成' : active(item) ? '进行中' : '已取消'}</Typography.Text>
              {item.error_message && <details><summary>{conciseError(item.error_message)}</summary><p>{item.error_message}</p></details>}
            </div>
          </List.Item>} /> }]} />
        </div>
      </List.Item>} />
      {canWrite && [...batches].map(([batch, failures]) => <Button key={batch} style={{ marginRight: 8, marginTop: 8 }} loading={retrying === batch} disabled={!!retrying || items.some(item => item.batch_id === batch && active(item))} onClick={() => void retry(batch)}>重试本批次失败项（{Math.max(failures[0].batch_failed_items || 0, failures.length)} 篇）</Button>)}
    </>,
  }]} />;
}
