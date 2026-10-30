import { useEffect, useId, useRef, useState, type ChangeEvent } from 'react';
import { Alert, Button, Input, Spin, Steps, Tag, Typography } from 'antd';
import { ArrowRightOutlined, BellOutlined, ExportOutlined, FileTextOutlined, LockOutlined, PlusOutlined, ReadOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { confirmExpansion, submitPreparation, type ExpansionDetail, type ExpansionPreparation, type PaperRef } from '../../api/paperWorkflows';
import { safePaperUrl } from '../../paper/model';
import MarkdownViewer from '../MarkdownViewer';
import { researchDepthLabels } from './ResearchDepth';

export const expansionStages = [
  { slot: 'expansion_plan', title: '准备参考论文与检索计划' },
  { slot: 'candidate_set', title: '拓展与筛选' },
  { slot: 'relation_analysis', title: '分析论文关系' },
  { slot: 'expansion_result', title: '研究脉络与阅读建议' },
];

function PreparationQuestion({ question, disabled, onChange }: {
  question: ExpansionPreparation['questions'][number]; disabled: boolean; onChange: (answer: string) => void;
}) {
  const id = useId();
  const options = question.options || [];
  const [value, setValue] = useState<{ choice?: number; custom: string; note: string }>({ custom: '', note: '' });
  const custom = !options.length || value.choice === -1;
  const update = (next: typeof value) => {
    setValue(next);
    const customAnswer = !options.length || next.choice === -1;
    const answer = customAnswer ? next.custom.trim() : next.choice !== undefined ? options[next.choice] : '';
    const note = customAnswer ? '' : next.note.trim();
    onChange(answer && note ? `${answer}\n补充说明：${note}` : answer || '');
  };
  return <fieldset className="paper-preparation-question" disabled={disabled}>
    <legend>{question.question}</legend>
    {!!options.length && <div className="paper-preparation-answer-options">
      {options.map((option, index) => {
        const separator = option.indexOf('：');
        return <label className="paper-preparation-answer-option" key={option}>
          <input type="radio" name={id} checked={value.choice === index} onChange={() => update({ ...value, choice: index })} />
          <span>{separator > 0 ? <><strong>{option.slice(0, separator)}</strong><span>{option.slice(separator + 1)}</span></> : <strong>{option}</strong>}</span>
        </label>;
      })}
      <label className="paper-preparation-answer-option paper-preparation-answer-custom">
        <input type="radio" name={id} checked={custom} onChange={() => update({ ...value, choice: -1 })} /><span>自行填写回答</span>
      </label>
      {custom && <div className="paper-preparation-custom-input"><Input.TextArea aria-label={`${question.question}（自行填写）`} placeholder="描述你关心的研究目标或应用场景…" disabled={disabled}
        autoSize={{ minRows: 3, maxRows: 6 }} value={value.custom} onChange={(event: ChangeEvent<HTMLTextAreaElement>) => update({ ...value, custom: event.target.value })} /></div>}
    </div>}
    {!options.length && <Input.TextArea aria-label={question.question} placeholder="填写你的回答…" disabled={disabled}
      autoSize={{ minRows: 3, maxRows: 6 }} value={value.custom} onChange={(event: ChangeEvent<HTMLTextAreaElement>) => update({ ...value, custom: event.target.value })} />}
    {!custom && <details className="paper-preparation-answer-note">
      <summary><PlusOutlined aria-hidden />补充说明（选填）</summary>
      <Input.TextArea aria-label={`${question.question}（补充说明）`} placeholder="补充更具体的关注点或限制条件…" disabled={disabled}
        autoSize={{ minRows: 3, maxRows: 6 }} value={value.note} onChange={(event: ChangeEvent<HTMLTextAreaElement>) => update({ ...value, note: event.target.value })} />
    </details>}
  </fieldset>;
}

function DirectionEvidence({ direction }: { direction: ExpansionPreparation['directions'][number] }) {
  const papers: PaperRef[] = direction.papers?.length ? direction.papers : direction.paper_ids.map(id => ({ paper_id: id, title: id, abstract: null }));
  return <div className="paper-preparation-direction-evidence">
    <span>{direction.paper_ids.length} 篇参考论文</span>
    <details><summary>查看依据</summary><div className="paper-preparation-evidence-content">
      <p>{direction.reason}</p>
      {direction.difference && <p className="paper-preparation-muted">{direction.difference}</p>}
      <ul>{papers.map(paper => {
        const url = [paper.paper_url, paper.doi && `https://doi.org/${paper.doi}`, paper.arxiv_id && `https://arxiv.org/abs/${paper.arxiv_id}`, paper.pdf_url].map(safePaperUrl).find(Boolean);
        const title = paper.title || paper.paper_id;
        return <li key={paper.paper_id}><FileTextOutlined aria-hidden />{url ? <a href={url} target="_blank" rel="noopener noreferrer">{title}<ExportOutlined aria-hidden /></a> : <span>{title}</span>}</li>;
      })}</ul>
    </div></details>
  </div>;
}

export default function PreparationPanel({ detail, onRefresh, onStart, embedded = false }: {
  detail: ExpansionDetail; onRefresh: () => Promise<void>; onStart: (conversation: string) => void; embedded?: boolean;
}) {
  const { t } = useTranslation();
  const record = detail.record;
  const review = record.goal === 'review';
  const preparation = record.preparation;
  const [answers, setAnswers] = useState<Record<string, string>>({});
  const [selected, setSelected] = useState<string[]>([]);
  const [instruction, setInstruction] = useState('');
  const [directionsExpanded, setDirectionsExpanded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [answerSubmitted, setAnswerSubmitted] = useState(false);
  const [error, setError] = useState('');
  const submitting = useRef(false);
  const requestKeys = useRef(new Map<string, string>());
  useEffect(() => {
    setAnswers({});
    setDirectionsExpanded(false);
    setAnswerSubmitted(false);
    setSelected(preparation?.selected_directions || []);
    setInstruction('');
    setError('');
  }, [record.id, record.preparation_revision, record.status]);
  const perform = async (action: 'answer' | 'revise' | 'retry' | 'confirm' | 'refresh') => {
    if (submitting.current || (action === 'answer' && answerSubmitted)) return;
    submitting.current = true;
    setBusy(true);
    setError('');
    try {
      if (action === 'refresh') { await onRefresh(); return; }
      const fingerprint = JSON.stringify([record.id, record.preparation_revision, action, answers, instruction.trim(), selected]);
      let key = requestKeys.current.get(fingerprint);
      if (!key) { key = crypto.randomUUID(); requestKeys.current.set(fingerprint, key); }
      const common = { revision: record.preparation_revision, request_key: key };
      if (action === 'confirm') {
        const result = await confirmExpansion(record.id, { ...common, selected_directions: selected });
        onStart(result.conversation_id);
      } else {
        await submitPreparation(record.id, { ...common, action, answers, user_instruction: instruction.trim() || undefined });
        if (action === 'answer') setAnswerSubmitted(true);
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
  const compact = embedded && !pending;
  const choices = preparation.directions || [];
  const confirmed = preparation.selected_directions || [];
  const summaryPreview = preparation.summary?.split('\n').find(line => line.trim() && !/^#{1,6}\s/.test(line)) || preparation.summary;
  const currentStage = expansionStages.findIndex(stage => !record.progress?.[stage.slot]);
  const preparationPhase = record.status === 'completed' ? 3 : record.status === 'awaiting_confirmation' ? 1 : pending ? 0 : 2;
  const confirmedChoices = choices.filter(direction => confirmed.includes(direction.name));
  const visibleChoices = compact && !directionsExpanded ? confirmedChoices.slice(0, 1) : confirmedChoices;
  const confirmedDirections = confirmed.length > 0 && <section className="paper-preparation-confirmed" aria-label="本次研究方向">
      <div className="paper-preparation-section-heading"><h3>本次研究方向</h3></div>
      {visibleChoices.map(direction => <article key={direction.name}>
        <strong>{direction.name}</strong><Typography.Paragraph ellipsis={compact ? { rows: 2, expandable: 'collapsible', symbol: (expanded: boolean) => t(expanded ? 'paperTasks.overview.collapse' : 'paperTasks.overview.expand') } : undefined}>{direction.question}</Typography.Paragraph>
        <DirectionEvidence direction={direction} />
      </article>)}
      {compact && confirmedChoices.length > 1 && <Button type="link" size="small" className="paper-preparation-all-directions" aria-expanded={directionsExpanded} onClick={() => setDirectionsExpanded(value => !value)}>{t(directionsExpanded ? 'paperTasks.overview.hideDirections' : 'paperTasks.overview.showDirections', { count: confirmedChoices.length })}</Button>}
    </section>;
  const content = <section className="paper-preparation" aria-label="初步调研与方向确认">
    {!compact && <div className="paper-preparation-meta"><LockOutlined />{researchDepthLabels[record.budget_mode || 'balanced']}调研</div>}
    {compact && confirmedDirections}
    {!review && !compact && <Steps className="paper-preparation-steps" size="small" current={preparationPhase}
      items={[{ title: '初步调研' }, { title: preparation.auto_confirmed ? '方向已确定' : '确认方向' }, { title: '正式拓展' }]} />}
    {record.status === 'preparing' && <div className="paper-preparation-working" role="status"><Spin size="small" /><div><strong>正在调研研究问题</strong><p>{review ? t('paperTasks.reviewFlow.researchingHint') : '你可以离开此页面，需要补充信息或选择方向时会通知你。只有一个方向时将自动继续拓展。'}</p></div></div>}
    {preparation.summary && !compact && <section className="paper-preparation-findings">
      <h3><ReadOutlined />{record.status === 'preparing' ? '已有调研材料' : '初步调研成果'}</h3>
      <Typography.Paragraph ellipsis={{ rows: 2 }}>{summaryPreview}</Typography.Paragraph>
      <details><summary>查看调研详情</summary><MarkdownViewer>{preparation.summary}</MarkdownViewer></details>
    </section>}
    {compact && preparation.summary && <details className="paper-preparation-background"><summary>{t('paperTasks.overview.showResearch')}</summary>
      <div className="paper-preparation-meta"><LockOutlined />{researchDepthLabels[record.budget_mode || 'balanced']}调研</div>
      <MarkdownViewer>{preparation.summary}</MarkdownViewer>
    </details>}
    {preparation.error && <Alert type="error" showIcon message={preparation.error} />}
    {record.status === 'awaiting_info' && <section className="paper-preparation-questions">
      <div className="paper-preparation-questions-heading"><h3>补充研究范围</h3><Tag color="blue" bordered={false}>{answerSubmitted ? '调研中' : '待你补充'}</Tag></div>
      {preparation.questions.map(question => <PreparationQuestion key={`${record.id}:${record.preparation_revision}:${record.status}:${question.id}`} question={question} disabled={busy || answerSubmitted}
        onChange={answer => setAnswers(current => ({ ...current, [question.id]: answer }))} />)}
      <div className="paper-preparation-answer-actions"><span aria-live="polite">{answerSubmitted ? '已提交，正在更新调研状态' : '补充后继续初步调研'}</span>
        <Button type="primary" aria-label={answerSubmitted ? '已提交' : '继续调研'} loading={busy} disabled={answerSubmitted || preparation.questions.some(question => !answers[question.id]?.trim())} onClick={() => void perform('answer')}>{answerSubmitted ? '已提交' : '继续调研'}<ArrowRightOutlined aria-hidden /></Button>
      </div>
    </section>}
    {record.status === 'awaiting_confirmation' && <>
      <div className="paper-preparation-section-heading"><h3>{review ? t('paperTasks.reviewFlow.chooseDirections') : '选择研究方向'}</h3><span>可多选 · {choices.length} 个候选方向</span></div>
      <div className="paper-preparation-directions">{choices.map(direction => <article className={`paper-preparation-direction${selected.includes(direction.name) ? ' is-selected' : ''}`} key={direction.name}>
        <label className="paper-preparation-direction-choice">
          <input type="checkbox" checked={selected.includes(direction.name)} aria-label={direction.name} onChange={event => setSelected(current => event.target.checked ? [...current, direction.name] : current.filter(name => name !== direction.name))} />
          <span><strong>{direction.name}</strong><span className="paper-preparation-direction-question">{direction.question}</span></span>
        </label>
        <DirectionEvidence direction={direction} />
      </article>)}</div>
    </>}
    {running && !compact && preparation.auto_confirmed && <Alert className="paper-preparation-auto" type="info" showIcon icon={<ArrowRightOutlined />} message={review ? t('paperTasks.reviewFlow.autoConfirmed') : '仅有一个研究方向，已自动继续拓展'} description="初步调研已完成，无需再次选择或确认。" />}
    {!pending && !compact && confirmedDirections}
    {running && !embedded && <>
      <div className="paper-preparation-section-heading"><h3>{review ? t('paperTasks.reviewFlow.stage.read') : '拓展进展'}</h3><span>{record.status === 'pending' ? '等待开始' : '后台执行中'}</span></div>
      <ol className="paper-preparation-progress">{expansionStages.map((stage, index) => <li key={stage.slot} data-status={record.progress?.[stage.slot] ? 'done' : record.status !== 'pending' && index === currentStage ? 'running' : 'pending'}>
        <span className="paper-preparation-progress-index">{index + 1}</span><span>{stage.title}</span><span className="paper-preparation-progress-status">{record.progress?.[stage.slot] ? '已完成' : record.status !== 'pending' && index === currentStage ? '进行中' : '待开始'}</span>
      </li>)}</ol>
      <div className="paper-preparation-running-footer"><span><BellOutlined />需要你处理或完成时会通知你。</span>{detail.chat_status === 'available' && <Button onClick={() => onStart(record.conversation_id)}>{review ? t('paperTasks.reviewFlow.openChat') : '打开拓展会话'}</Button>}</div>
    </>}
    {record.status === 'preparation_failed' && <Button loading={busy} onClick={() => void perform('retry')}>在原记录中重试初步调研</Button>}
    {['awaiting_confirmation', 'preparation_failed'].includes(record.status) && <details className="paper-preparation-adjust"><summary>调整研究范围</summary><div>
      <Input.TextArea aria-label="调整研究范围" placeholder="补充你更关心的问题，系统会先完成补充调研。" autoSize={{ minRows: 3, maxRows: 8 }} value={instruction} onChange={(event: ChangeEvent<HTMLTextAreaElement>) => setInstruction(event.target.value)} />
      <Button disabled={!instruction.trim()} loading={busy} onClick={() => void perform('revise')}>更新范围并补充调研</Button>
    </div></details>}
    {error && <Alert type="error" showIcon message={error} action={answerSubmitted ? <Button size="small" loading={busy} onClick={() => void perform('refresh')}>刷新状态</Button> : undefined} />}
    {record.status === 'awaiting_confirmation' && <footer className="paper-preparation-footer"><span aria-live="polite"><strong>已选 {selected.length} 个方向</strong><span>{instruction.trim() ? '请先完成补充调研' : review ? t('paperTasks.reviewFlow.confirmHint') : '确认后开始正式拓展'}</span></span>
      <Button aria-label={review ? t('paperTasks.reviewFlow.confirmButton') : '确认方向并开始拓展'} type="primary" loading={busy} disabled={!selected.length || !!instruction.trim()} onClick={() => void perform('confirm')}>{review ? t('paperTasks.reviewFlow.confirmButton') : '确认并开始拓展'}<ArrowRightOutlined /></Button>
    </footer>}
  </section>;
  return embedded || pending || running ? content : <details className="paper-preparation-history"><summary>初步调研与方向确认</summary>{content}</details>;
}
