import { useState } from 'react';
import { Button, Tooltip, message } from 'antd';
import { StarFilled, StarOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { setPaperImportance } from '../../api/paperWorkflows';

export default function PaperImportanceButton({ paperID, important, onChange }: {
    paperID: string;
    important: boolean;
    onChange: () => Promise<void>;
}) {
    const { t } = useTranslation();
    const [busy, setBusy] = useState(false);
    const label = t(important ? 'paperTasks.unmarkImportant' : 'paperTasks.markImportant');
    const toggle = async () => {
        if (busy) return;
        setBusy(true);
        try {
            await setPaperImportance(paperID, !important);
            await onChange();
        } catch {
            message.error(t('paperTasks.importanceFailed'));
        } finally {
            setBusy(false);
        }
    };
    return <Tooltip title={label}><Button type="text" aria-label={label} aria-pressed={important}
        loading={busy} disabled={busy} icon={important ? <StarFilled style={{ color: '#faad14' }} /> : <StarOutlined />}
        onClick={() => void toggle()} /></Tooltip>;
}
