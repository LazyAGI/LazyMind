import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Empty, Pagination, Spin, Tag } from 'antd';
import { useTranslation } from 'react-i18next';
import { listScheduleTasks, type Task } from './api';
import { taskStatusDescription } from './taskStatusDescription';

export default function ScheduleRunHistory({ scheduleId, currentId, onSelect }: {
  scheduleId: string; currentId: string; onSelect: (task: Task) => void;
}) {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);
  const [result, setResult] = useState<{ items: Task[]; total: number; page: number }>({ items: [], total: 0, page: 1 });
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [version, setVersion] = useState(0);
  const contentRef = useRef<HTMLDivElement>(null);
  const [contentHeight, setContentHeight] = useState<number>();
  useEffect(() => {
    let active = true;
    setLoading(true); setFailed(false);
    listScheduleTasks(scheduleId, page, 5).then(value => {
      if (active) setResult({ ...value, page });
    }).catch(() => { if (active) setFailed(true); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [scheduleId, page, version]);
  const items = result.page === page ? result.items : [];
  const changePage = (nextPage: number) => {
    // Keep the drawer from collapsing while rows load or on a shorter last page.
    setContentHeight(contentRef.current?.getBoundingClientRect().height);
    setPage(nextPage);
  };

  return <section className='task-detail-section task-run-history' aria-label={t('taskCenter.executionHistory')} aria-busy={loading}>
    <div className='task-run-history-heading'><h3>{t('taskCenter.executionHistory')}</h3><span>{t('taskCenter.executionHistoryHint')}</span></div>
    {failed && <Alert type='warning' showIcon message={t('taskCenter.loadError')} action={<Button size='small' onClick={() => setVersion(v => v + 1)}>{t('common.retry')}</Button>} />}
    <Spin spinning={loading}>
      <div ref={contentRef} style={{ minHeight: contentHeight }}>
      {items.length ? <div className='task-run-table-scroll'><table className='task-run-table' aria-label={t('taskCenter.executionHistory')}>
        <colgroup><col className='task-run-sequence' /><col className='task-run-status' /><col /><col /></colgroup>
        <thead><tr><th scope='col'>{t('taskCenter.sequence')}</th><th scope='col'>{t('taskCenter.statusCol')}</th><th scope='col'>{t('taskCenter.createdAt')}</th><th scope='col'>{t('taskCenter.finishedAt')}</th></tr></thead>
        <tbody>{items.map((run, index) => <tr key={run.id}>
          <td><Button type='link' disabled={loading} aria-label={`${currentId === run.id ? t('taskCenter.currentExecution') : t('taskCenter.viewExecution')} · ${new Date(run.created_at).toLocaleString()}`} aria-current={currentId === run.id ? 'true' : undefined} onClick={() => onSelect(run)}>{result.total - (page - 1) * 5 - index}</Button></td>
          <td><div className='schedule-history-status'>
            <Tag color={['succeeded', 'completed'].includes(run.status) ? 'success' : run.status === 'failed' ? 'error' : run.status === 'running' ? 'processing' : 'default'}>
              {t(`taskCenter.status${['succeeded', 'completed'].includes(run.status) ? 'Success' : run.status === 'waiting_inputs' ? 'WaitingInputs' : run.status.charAt(0).toUpperCase() + run.status.slice(1)}`, { defaultValue: t('taskCenter.statusUnknown') })}
            </Tag><small>{taskStatusDescription(run, t)}</small>
          </div></td>
          <td><time dateTime={run.created_at}>{new Date(run.created_at).toLocaleString(undefined, { hour12: false })}</time></td>
          <td>{run.finished_at ? <time dateTime={run.finished_at}>{new Date(run.finished_at).toLocaleString(undefined, { hour12: false })}</time> : '—'}</td>
        </tr>)}</tbody>
      </table></div> : !loading && !failed ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('taskCenter.noExecutions')} /> : null}
      </div>
    </Spin>
    {result.total > 5 && <Pagination size='small' current={page} pageSize={5} total={result.total} showSizeChanger={false} disabled={loading} onChange={changePage} />}
  </section>;
}
