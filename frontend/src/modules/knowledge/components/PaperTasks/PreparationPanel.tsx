import { useEffect, useRef, useState, type ChangeEvent } from 'react';
import { Alert, Button, Input, Space, Spin, Steps, Typography } from 'antd';
import { ArrowRightOutlined, BellOutlined, FileTextOutlined, LockOutlined, ReadOutlined } from '@ant-design/icons';
import { confirmExpansion, submitPreparation, type ExpansionDetail } from '../../api/paperWorkflows';
import MarkdownViewer from '../MarkdownViewer';
import { researchDepthLabels } from './ResearchDepth';

const stages = [
  { slot: 'expansion_plan', title: '准备参考论文与检索计划' },
  { slot: 'candidate_set', title: '拓展与筛选' },
  { slot: 'relation_analysis', title: '分析论文关系' },
  { slot: 'expansion_result', title: '研究脉络与阅读建议' },
];

export default function PreparationPanel({ detail, onRefresh, onStart }: {
  detail: ExpansionDetail; onRefresh: () => Promise<void>; onStart: (conversation: string) => void;
}) {
  const record = detail.record;
  const preparation = record.preparation;
  const [answers, setAnswers] = useState<Record<string, string>>({});
  const [selected, setSelected] = useState<string[]>([]);
  const [instruction, setInstruction] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const submitting = useRef(false);
  const requestKeys = useRef(new Map<string, string>());
  useEffect(() => {
    setAnswers({});
    setSelected(preparation?.selected_directions || []);
    setInstruction('');
    setError('');
  }, [record.id, record.preparation_revision, record.status]);
  const perform = async (action: 'answer' | 'revise' | 'retry' | 'confirm') => {
    if (submitting.current) return;
    submitting.current = true;
    setBusy(true);
    setError('');
    try {
      const fingerprint = JSON.stringify([record.id, record.preparation_revision, action, answers, instruction.trim(), selected]);
      let key = requestKeys.current.get(fingerprint);
      if (!key) { key = crypto.randomUUID(); requestKeys.current.set(fingerprint, key); }
      const common = { revision: record.preparation_revision, request_key: key };
      if (action === 'confirm') {
        const result = await confirmExpansion(record.id, { ...common, selected_directions: selected });
        onStart(result.conversation_id);
      } else {
        await submitPreparation(record.id, { ...common, action, answers, user_instruction: instruction.trim() || undefined });
        await onRefresh();
      }
    } catch (problem) {
      setError(problem instanceof Error ? problem.message : '操作失败，请刷新后重试');
    } finally {
      submitting.current = false;
      setBusy(false);
    }
  };
  if (!preparation) return null;
  const pending = ['preparing', 'awaiting_info', 'awaiting_confirmation', 'preparation_failed'].includes(record.status);
  const running = ['pending', 'running', 'active'].includes(record.status);
  const choices = preparation.directions || [];
  const confirmed = preparation.selected_directions || [];
  const summaryPreview = preparation.summary?.split('\n').find(line => line.trim() && !/^#{1,6}\s/.test(line)) || preparation.summary;
  const currentStage = stages.findIndex(stage => !record.progress?.[stage.slot]);
  const preparationPhase = record.status === 'completed' ? 3 : record.status === 'awaiting_confirmation' ? 1 : pending ? 0 : 2;
  const content = <section className="paper-preparation" aria-label="初步调研与方向确认">
    <div className="paper-preparation-meta"><LockOutlined />{researchDepthLabels[record.budget_mode || 'balanced']}调研</div>
    <Steps className="paper-preparation-steps" size="small" current={preparationPhase}
      items={[{ title: '初步调研' }, { title: preparation.auto_confirmed ? '方向已确定' : '确认方向' }, { title: '正式拓展' }]} />
    {record.status === 'preparing' && <div className="paper-preparation-working" role="status"><Spin size="small" /><div><strong>正在调研研究问题</strong><p>你可以离开此页面，需要补充信息或选择方向时会通知你。只有一个方向时将自动继续拓展。</p></div></div>}
    {preparation.summary && <section className="paper-preparation-findings">
      <h3><ReadOutlined />{record.status === 'preparing' ? '已有调研材料' : '初步调研成果'}</h3>
      <Typography.Paragraph ellipsis={{ rows: 2 }}>{summaryPreview}</Typography.Paragraph>
      <details><summary>查看调研详情</summary><MarkdownViewer>{preparation.summary}</MarkdownViewer></details>
    </section>}
    {preparation.error && <Alert type="error" showIcon message={preparation.error} />}
    {record.status === 'awaiting_info' && <section className="paper-preparation-questions">
      <h3>补充研究范围</h3>
      {preparation.questions.map(question => <div className="paper-preparation-question" key={question.id}>
        <label htmlFor={`paper-preparation-${question.id}`}>{question.question}</label>
        {!!question.options?.length && <Space wrap>{question.options.map(option => <Button key={option} size="small" type={answers[question.id] === option ? 'primary' : 'default'} onClick={() => setAnswers(current => ({ ...current, [question.id]: option }))}>{option}</Button>)}</Space>}
        <Input.TextArea id={`paper-preparation-${question.id}`} aria-label={question.question} autoSize={{ minRows: 2, maxRows: 6 }} value={answers[question.id] || ''} onChange={(event: ChangeEvent<HTMLTextAreaElement>) => setAnswers(current => ({ ...current, [question.id]: event.target.value }))} />
      </div>)}
      <Button type="primary" loading={busy} disabled={preparation.questions.some(question => !answers[question.id]?.trim())} onClick={() => void perform('answer')}>提交补充并继续调研</Button>
    </section>}
    {record.status === 'awaiting_confirmation' && <>
      <div className="paper-preparation-section-heading"><h3>选择研究方向</h3><span>可多选 · {choices.length} 个候选方向</span></div>
      <div className="paper-preparation-directions">{choices.map(direction => <article className={`paper-preparation-direction${selected.includes(direction.name) ? ' is-selected' : ''}`} key={direction.name}>
        <label className="paper-preparation-direction-choice">
          <input type="checkbox" checked={selected.includes(direction.name)} aria-label={direction.name} onChange={event => setSelected(current => event.target.checked ? [...current, direction.name] : current.filter(name => name !== direction.name))} />
          <span><strong>{direction.name}</strong><span className="paper-preparation-direction-question">{direction.question}</span></span>
        </label>
        <div className="paper-preparation-direction-evidence">
          <span>{direction.paper_ids.length} 篇代表论文</span>
          <details><summary>查看依据</summary><div className="paper-preparation-evidence-content">
            <p>{direction.reason}</p>
            {direction.difference && <p className="paper-preparation-muted">{direction.difference}</p>}
            <ul>{(direction.papers?.length ? direction.papers : direction.paper_ids.map(id => ({ paper_id: id, title: id }))).map(paper => <li key={paper.paper_id}><FileTextOutlined /><span>{paper.title || paper.paper_id}</span></li>)}</ul>
          </div></details>
        </div>
      </article>)}</div>
    </>}
    {running && <>
      {preparation.auto_confirmed && <Alert className="paper-preparation-auto" type="info" showIcon icon={<ArrowRightOutlined />} message="仅有一个研究方向，已自动继续拓展" description="初步调研已完成，无需再次选择或确认。" />}
      <section className="paper-preparation-confirmed"><div className="paper-preparation-section-heading"><h3>本次研究方向</h3></div>{choices.filter(direction => confirmed.includes(direction.name)).map(direction => <div key={direction.name}><strong>{direction.name}</strong><p>{direction.question}</p></div>)}</section>
      <div className="paper-preparation-section-heading"><h3>拓展进展</h3><span>{record.status === 'pending' ? '等待开始' : '后台执行中'}</span></div>
      <ol className="paper-preparation-progress">{stages.map((stage, index) => <li key={stage.slot} data-status={record.progress?.[stage.slot] ? 'done' : record.status !== 'pending' && index === currentStage ? 'running' : 'pending'}>
        <span className="paper-preparation-progress-index">{index + 1}</span><span>{stage.title}</span><span className="paper-preparation-progress-status">{record.progress?.[stage.slot] ? '已完成' : record.status !== 'pending' && index === currentStage ? '进行中' : '待开始'}</span>
      </li>)}</ol>
      <div className="paper-preparation-running-footer"><span><BellOutlined />需要你处理或完成时会通知你。</span>{detail.chat_status === 'available' && <Button onClick={() => onStart(record.conversation_id)}>打开拓展会话</Button>}</div>
    </>}
    {record.status === 'preparation_failed' && <Button loading={busy} onClick={() => void perform('retry')}>在原记录中重试初步调研</Button>}
    {record.status !== 'preparing' && !running && <details className="paper-preparation-adjust"><summary>调整研究范围</summary><div>
      <Input.TextArea aria-label="调整研究范围" placeholder="补充你更关心的问题，系统会先完成补充调研。" autoSize={{ minRows: 3, maxRows: 8 }} value={instruction} onChange={(event: ChangeEvent<HTMLTextAreaElement>) => setInstruction(event.target.value)} />
      <Button disabled={!instruction.trim()} loading={busy} onClick={() => void perform('revise')}>更新范围并补充调研</Button>
    </div></details>}
    {error && <Alert type="error" showIcon message={error} />}
    {record.status === 'awaiting_confirmation' && <footer className="paper-preparation-footer"><span aria-live="polite"><strong>已选 {selected.length} 个方向</strong><span>{instruction.trim() ? '请先完成补充调研' : '确认后开始正式拓展'}</span></span>
      <Button aria-label="确认方向并开始拓展" type="primary" loading={busy} disabled={!selected.length || !!instruction.trim()} onClick={() => void perform('confirm')}>确认并开始拓展<ArrowRightOutlined /></Button>
    </footer>}
  </section>;
  return pending || running ? content : <details className="paper-preparation-history"><summary>初步调研与方向确认</summary>{content}</details>;
}
