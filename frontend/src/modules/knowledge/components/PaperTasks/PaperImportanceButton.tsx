import { useState } from 'react';
import { Button, Tooltip, message } from 'antd';
import { StarFilled, StarOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';

export default function PaperImportanceButton({important,onToggle,onError,disabled = false}:{important:boolean;disabled?:boolean;onToggle:(important:boolean)=>Promise<void>;onError?:(error:unknown)=>void}) {
    const { t } = useTranslation();
    const [busy, setBusy] = useState(false);
    const label = t(important ? 'paperTasks.unmarkImportant' : 'paperTasks.markImportant');
    const toggle = async () => {
        if (busy || disabled) return;
        setBusy(true);
        try {
            await onToggle(!important);
        } catch (error) {
            if (onError) onError(error);
            else message.error(t('paperTasks.importanceFailed'));
        } finally {
            setBusy(false);
        }
    };
    return <Tooltip title={label}><Button type="text" aria-label={label} aria-pressed={important}
        loading={busy} disabled={busy || disabled} icon={important ? <StarFilled style={{ color: '#faad14' }} /> : <StarOutlined />}
        onClick={() => void toggle()} /></Tooltip>;
}
