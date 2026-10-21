import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Modal, Spin, Typography, message } from 'antd';
import { useTranslation } from 'react-i18next';
import { getTaskRemovalPreview, removeTask, type Task, type TaskRemovalPreview } from './api';

export default function TaskRemovalDialog({ task, onClose }: { task: Task | null; onClose: () => void }) {
  const { t } = useTranslation();
  const [preview, setPreview] = useState<TaskRemovalPreview>();
  const [loading, setLoading] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [failed, setFailed] = useState(false);
  const [scopeChanged, setScopeChanged] = useState(false);
  const [revision, setRevision] = useState(0);
  const inFlight = useRef(false);
  const generation = useRef(0);
  useEffect(() => {
    const version = ++generation.current;
    setPreview(undefined); setFailed(false); setScopeChanged(false);
    if (!task) return;
    const controller = new AbortController();
    setLoading(true);
    getTaskRemovalPreview(task.id, controller.signal).then(value => {
      if (generation.current === version) setPreview(value);
    }).catch(() => {
      if (!controller.signal.aborted && generation.current === version) setFailed(true);
    }).finally(() => { if (generation.current === version) setLoading(false); });
    return () => { controller.abort(); ++generation.current; };
  }, [task?.id, revision]);
  const submit = async () => {
    if (!task || !preview || inFlight.current) return;
    const version = generation.current;
    inFlight.current = true; setSubmitting(true); setFailed(false);
    try {
      const result = await removeTask(task.id, preview.impact_version);
      message.success(t(result.cancellation_pending ? 'taskCenter.removalStopping' : result.trashed_conversation_ids.length ? 'taskCenter.taskRemoveSuccess' : 'taskCenter.removalOrphanSuccess'));
      if (generation.current === version) onClose();
    } catch (error) {
      if (generation.current !== version) return;
      if ((error as { response?: { data?: { code?: number } } }).response?.data?.code === 2004811) {
        setScopeChanged(true); setPreview(undefined);
        try { const next = await getTaskRemovalPreview(task.id); if (generation.current === version) setPreview(next); }
        catch { if (generation.current === version) setFailed(true); }
      } else setFailed(true);
    } finally { inFlight.current = false; setSubmitting(false); }
  };
  return <Modal open={Boolean(task)} title={t('taskCenter.removalTitle')} onCancel={() => { if (!inFlight.current) onClose(); }}
    closable={!submitting} maskClosable={!submitting} keyboard={!submitting} confirmLoading={submitting}
    okText={t('settingsPage.recovery.moveToTrash')} cancelText={t('common.cancel')}
    okButtonProps={{ danger: true, disabled: !preview || loading, 'aria-label': t('settingsPage.recovery.moveToTrash') }} cancelButtonProps={{ disabled: submitting }} onOk={() => void submit()}>
    <Typography.Paragraph ellipsis={{ rows: 2, tooltip: true }} strong>{task?.conversation_title || task?.title || t('taskCenter.noTitle')}</Typography.Paragraph>
    <Spin spinning={loading}>
      {scopeChanged && <Alert type='warning' showIcon message={t('taskCenter.removalScopeChanged')} />}
      {failed && <Alert type='error' showIcon message={t('taskCenter.taskRemoveFailed')}
        action={!preview ? <Button size='small' onClick={() => setRevision(v => v + 1)}>{t('common.retry')}</Button> : undefined} />}
      {preview && <div aria-live='polite'>
        <p>{t('taskCenter.removalImpact', { tasks: preview.task_count, conversations: preview.conversation_count })}</p>
        {preview.active_task_count > 0 && <p>{t('taskCenter.removalActiveImpact', { count: preview.active_task_count })}</p>}
        <p>{t(preview.conversation_count ? 'taskCenter.removalRetention' : 'taskCenter.removalOrphanHint')}</p>
      </div>}
    </Spin>
  </Modal>;
}
