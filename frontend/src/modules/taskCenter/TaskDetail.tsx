import { Button, Drawer, Empty, Progress, Tag } from 'antd';
import { CheckCircleFilled, ClockCircleOutlined, CloseOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import type { Task } from './api';

interface TaskDetailProps {
  task: Task | null;
  onClose: () => void;
  onOpenConversation: (conversationId: string) => void;
}

const isDone = (status: string) => ['completed', 'succeeded'].includes(status);

export default function TaskDetail({ task, onClose, onOpenConversation }: TaskDetailProps) {
  const { t } = useTranslation();
  const steps = task?.steps ?? [];
  const completed = steps.filter((step) => isDone(step.status)).length;

  return (
    <Drawer
      className='task-detail-drawer'
      title={t('taskCenter.taskDetail')}
      width={480}
      open={Boolean(task)}
      onClose={onClose}
      closeIcon={<CloseOutlined />}
      footer={task ? (
        <Button type='primary' block size='large' onClick={() => onOpenConversation(task.conversation_id)}>
          {t('taskCenter.openConversation')}
        </Button>
      ) : null}
    >
      {task ? (
        <div className='task-detail-content'>
          <div className='task-detail-heading'>
            <div className={`task-type-icon task-type-${task.task_type}`}><ClockCircleOutlined /></div>
            <div className='task-detail-title-wrap'>
              <h2>{task.conversation_title || task.title || t('taskCenter.noTitle')}</h2>
              <span>{formatDate(task.created_at)}</span>
            </div>
            <StatusTag status={task.status} />
          </div>

          <section className='task-detail-section task-detail-description'>
            <span className='section-kicker'>{t('taskCenter.taskDescriptionCol')}</span>
            <p>{task.title || task.conversation_title || t('taskCenter.noDescription')}</p>
          </section>

          <section className='task-detail-section'>
            <h3>{t('taskCenter.executionProcess')}</h3>
            {steps.length ? (
              <>
                <Progress percent={Math.round((completed / steps.length) * 100)} showInfo={false} />
                <div className='task-step-list'>
                  {steps.map((step, index) => (
                    <div className={`task-step ${isDone(step.status) ? 'is-done' : step.status === 'running' ? 'is-running' : ''}`} key={`${step.step_id}-${index}`}>
                      <span className='task-step-dot'>{isDone(step.status) ? <CheckCircleFilled /> : index + 1}</span>
                      <div><strong>{step.step_id || `${t('taskCenter.steps')} ${index + 1}`}</strong><small>{step.status}</small></div>
                    </div>
                  ))}
                </div>
              </>
            ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('taskCenter.noSteps')} />}
          </section>

          <section className='task-detail-section task-detail-meta'>
            <h3>{t('taskCenter.taskInfo')}</h3>
            <dl>
              <div><dt>{t('taskCenter.taskType')}</dt><dd>{task.schedule_name || task.task_type}</dd></div>
              <div><dt>{t('taskCenter.createdAt')}</dt><dd>{formatDate(task.created_at)}</dd></div>
              <div><dt>{t('taskCenter.finishedAt')}</dt><dd>{task.finished_at ? formatDate(task.finished_at) : '—'}</dd></div>
            </dl>
          </section>
        </div>
      ) : null}
    </Drawer>
  );
}

export function StatusTag({ status }: { status: string }) {
  const { t } = useTranslation();
  const color = isDone(status) ? 'success' : status === 'failed' ? 'error' : status === 'running' ? 'processing' : 'warning';
  const key = status === 'succeeded' ? 'Completed' : `${status.charAt(0).toUpperCase()}${status.slice(1)}`;
  return <Tag color={color}>{t(`taskCenter.status${key}`, { defaultValue: status })}</Tag>;
}

export function formatDate(value?: string) {
  return value ? new Date(value).toLocaleString() : '—';
}
