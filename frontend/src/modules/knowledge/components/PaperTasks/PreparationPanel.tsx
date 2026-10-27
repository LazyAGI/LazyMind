import { useEffect, useRef, useState, type ChangeEvent } from 'react';
import { Alert, Button, Checkbox, Input, Select, Space, Typography } from 'antd';
import { confirmExpansion, submitPreparation, type BudgetMode, type ExpansionDetail } from '../../api/paperWorkflows';

export const budgetOptions = [
  { value: 'fast', label: '快速：初步调研 2 分钟／正式拓展 10 分钟' },
  { value: 'balanced', label: '均衡：初步调研 3 分钟／正式拓展 20 分钟' },
  { value: 'deep', label: '深入：初步调研 5 分钟／正式拓展 30 分钟' },
];

export default function PreparationPanel({ detail, onRefresh, onStart }: {
  detail: ExpansionDetail; onRefresh: () => Promise<void>; onStart: (conversation: string) => void;
}) {
  const record = detail.record;
  const preparation = record.preparation;
  const [answers, setAnswers] = useState<Record<string, string>>({});
  const [selected, setSelected] = useState<string[]>([]);
  const [instruction, setInstruction] = useState('');
  const [budget, setBudget] = useState<BudgetMode>(record.budget_mode || 'balanced');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const requestKeys = useRef(new Map<string, string>());
  useEffect(() => {
    setAnswers({}); setSelected(preparation?.selected_directions || []);
    setBudget(record.budget_mode || 'balanced'); setInstruction(''); setError('');
  }, [record.id, record.preparation_revision, record.status]);
  const perform = async (action: 'answer' | 'revise' | 'retry' | 'confirm') => {
    if (busy) return;
    setBusy(true); setError('');
    try {
      const fingerprint = JSON.stringify([record.id, record.preparation_revision, action, answers, instruction.trim(), selected, budget]);
      let key = requestKeys.current.get(fingerprint);
      if (!key) { key = crypto.randomUUID(); requestKeys.current.set(fingerprint, key); }
      const common = { revision: record.preparation_revision, request_key: key, budget_mode: budget };
      if (action === 'confirm') {
        const result = await confirmExpansion(record.id, { ...common, selected_directions: selected });
        onStart(result.conversation_id);
      } else {
        await submitPreparation(record.id, { ...common, action, answers, user_instruction: instruction.trim() || undefined });
        await onRefresh();
      }
    } catch (problem) { setError(problem instanceof Error ? problem.message : '操作失败，请刷新后重试'); }
    finally { setBusy(false); }
  };
  if (!preparation) return null;
  const pending = ['preparing', 'awaiting_info', 'awaiting_confirmation', 'preparation_failed'].includes(record.status);
  const content = <Space direction="vertical" style={{ width: '100%', marginBottom: 16 }}>
    {preparation.summary && <Typography.Paragraph style={{ whiteSpace: 'pre-wrap' }}>{preparation.summary}</Typography.Paragraph>}
    {record.status === 'preparing' && <Alert type="info" showIcon message="初步调研已开始，你可以离开此页面。需要补充信息或确认方向时，我们会提醒你。" />}
    {preparation.error && <Alert type="error" message={preparation.error} />}
    {record.status === 'awaiting_info' && <>
      {preparation.questions.map(question => <div key={question.id}>
        <Typography.Paragraph>{question.question}</Typography.Paragraph>
        {!!question.options?.length && <Space wrap>{question.options.map(option => <Button key={option} size="small" onClick={() => setAnswers(current => ({ ...current, [question.id]: option }))}>{option}</Button>)}</Space>}
        <Input.TextArea aria-label={question.question} value={answers[question.id] || ''} onChange={(event: ChangeEvent<HTMLTextAreaElement>) => setAnswers(current => ({ ...current, [question.id]: event.target.value }))} />
      </div>)}
      <Button type="primary" loading={busy} disabled={preparation.questions.some(question => !answers[question.id]?.trim())} onClick={() => void perform('answer')}>提交补充并继续调研</Button>
    </>}
    {record.status === 'awaiting_confirmation' && <>
      <Checkbox.Group value={selected} onChange={(values: (string | number | boolean)[]) => setSelected(values.map(String))}>
        <Space direction="vertical">{preparation.directions.map(direction => <div key={direction.name}>
          <Checkbox value={direction.name}><strong>{direction.name}</strong></Checkbox>
          <Typography.Paragraph>{direction.question} {direction.reason}</Typography.Paragraph>
          {direction.difference && <Typography.Paragraph type="secondary">{direction.difference}</Typography.Paragraph>}
          <Typography.Paragraph type="secondary">代表论文：{(direction.papers || []).map(paper => paper.title || paper.paper_id).join('；') || direction.paper_ids.join('；')}</Typography.Paragraph>
        </div>)}</Space>
      </Checkbox.Group>
      <Select aria-label="本轮执行预算" value={budget} options={budgetOptions} onChange={setBudget} style={{ width: '100%' }} />
      <Button aria-label="确认方向并开始拓展" type="primary" loading={busy} disabled={!selected.length || !!instruction.trim()} onClick={() => void perform('confirm')}>确认方向并开始拓展</Button>
    </>}
    {record.status === 'preparation_failed' && <Button loading={busy} onClick={() => void perform('retry')}>在原记录中重试初步调研</Button>}
    {record.status !== 'preparing' && !['pending', 'running', 'active'].includes(record.status) && <>
      <Input.TextArea aria-label="调整研究范围" placeholder="调整研究问题或增加新方向后，先补充调研再确认" value={instruction} onChange={(event: ChangeEvent<HTMLTextAreaElement>) => setInstruction(event.target.value)} />
      <Button disabled={!instruction.trim()} loading={busy} onClick={() => void perform('revise')}>更新范围并补充调研</Button>
    </>}
    {error && <Alert type="error" message={error} />}
  </Space>;
  return pending ? content : <details><summary>初步调研与方向确认</summary>{content}</details>;
}
