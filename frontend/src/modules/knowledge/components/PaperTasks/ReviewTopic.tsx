import { useEffect, useId, useRef, useState, type ChangeEvent } from 'react';
import { Alert, Button, Input, Modal, Typography } from 'antd';
import { useTranslation } from 'react-i18next';
import axios from 'axios';
import { generateReviewTopic, type ReviewTopicInput } from '../../api/paperWorkflows';

export default function ReviewTopic({ value, onChange, input, disabled, onBusyChange }: {
    value: string; onChange: (value: string) => void; input: ReviewTopicInput;
    disabled?: boolean; onBusyChange: (busy: boolean) => void;
}) {
    const { t } = useTranslation();
    const id = useId();
    const [modal, contextHolder] = Modal.useModal();
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState('');
    const request = useRef<AbortController>();
    const confirming = useRef(false);
    useEffect(() => () => { request.current?.abort(); onBusyChange(false); }, [onBusyChange]);
    const generate = async () => {
        if (request.current || confirming.current || disabled) return;
        if (value.trim()) {
            confirming.current = true;
            const replace = await modal.confirm({ title: t('paperTasks.replaceTopicTitle'), content: t('paperTasks.replaceTopicHint'),
                okText: t('paperTasks.replaceTopic'), cancelText: t('paperTasks.cancel') });
            confirming.current = false;
            if (!replace) return;
        }
        const previous = value;
        const controller = new AbortController();
        request.current = controller;
        setBusy(true); onBusyChange(true); setError(''); onChange('');
        try {
            const topic = await generateReviewTopic(input, controller.signal);
            if (!controller.signal.aborted) onChange(topic);
        } catch (cause) {
            if (!controller.signal.aborted) {
                onChange(previous);
                setError(axios.isAxiosError(cause) && typeof cause.response?.data?.message === 'string'
                    ? cause.response.data.message : t('paperTasks.topicFailed'));
            }
        } finally {
            if (!controller.signal.aborted) { request.current = undefined; setBusy(false); onBusyChange(false); }
        }
    };
    return <div className="paper-review-field">
        {contextHolder}
        <div className="paper-review-topic-heading"><label htmlFor={id}>{t('paperTasks.reviewTopic')} <Typography.Text type="secondary">{t('paperTasks.optional')}</Typography.Text></label>
            <Button size="small" loading={busy} disabled={disabled} onClick={() => void generate()}>{t('paperTasks.aiTopic')}</Button></div>
        <Input id={id} value={value} disabled={busy || disabled} placeholder={t('paperTasks.topicSuggestionPlaceholder')} onChange={(event: ChangeEvent<HTMLInputElement>) => onChange(event.target.value)} />
        <Typography.Text type="secondary">{t('paperTasks.topicSuggestionHint')}</Typography.Text>
        {error && <Alert type="error" showIcon message={error} />}
    </div>;
}
