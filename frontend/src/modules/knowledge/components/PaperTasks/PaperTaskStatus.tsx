import type { ReactNode } from 'react';
import { Tag } from 'antd';
import { CheckCircleOutlined, ClockCircleOutlined, CloseCircleOutlined, LoadingOutlined, MinusCircleOutlined } from '@ant-design/icons';

export default function PaperTaskStatus({ status, children }: { status?: string; children: ReactNode }) {
    const running = ['running', 'active', 'preparing'].includes(status || '');
    const failed = ['failed', 'preparation_failed'].includes(status || '');
    const waiting = ['awaiting_info', 'awaiting_confirmation', 'awaiting_input', 'pending'].includes(status || '');
    const completed = status === 'completed';
    const stopped = ['stopped', 'canceled', 'cancelled'].includes(status || '');
    return <Tag className="paper-task-status" role="status" bordered={false}
        color={running ? 'processing' : failed ? 'error' : completed ? 'success' : waiting ? 'warning' : undefined}
        icon={running ? <LoadingOutlined /> : completed ? <CheckCircleOutlined /> : failed ? <CloseCircleOutlined />
            : waiting ? <ClockCircleOutlined /> : stopped ? <MinusCircleOutlined /> : undefined}>
        {children}
    </Tag>;
}
