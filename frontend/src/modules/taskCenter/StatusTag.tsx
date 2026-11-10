import type { MouseEvent } from 'react';
import { Tag } from 'antd';
import { useTranslation } from 'react-i18next';

export function StatusTag({ status, onClick }: { status: string; onClick?: () => void }) {
  const { t } = useTranslation();
  const color = ['completed', 'succeeded'].includes(status)
    ? 'success'
    : ['failed', 'interrupted'].includes(status)
      ? 'error'
      : status === 'running'
        ? 'processing'
        : status === 'paused'
          ? 'gold'
          : status === 'canceled'
            ? 'default'
            : 'orange';
  const key = status === 'succeeded' ? 'Completed' : status === 'interrupted' ? 'Failed' : status === 'waiting_inputs' ? 'WaitingInputs' : `${status.charAt(0).toUpperCase()}${status.slice(1)}`;
  return <Tag className={`task-status-tag status-${status}${onClick ? ' clickable-status' : ''}`} color={color} onClick={(event: MouseEvent<HTMLElement>) => { event.stopPropagation(); onClick?.(); }}>{t(`taskCenter.status${key}`, { defaultValue: status })}</Tag>;
}
