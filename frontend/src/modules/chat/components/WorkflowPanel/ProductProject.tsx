import { useEffect, useRef, useState, type ChangeEvent } from 'react';
import { Alert, Button, Dropdown, Input, Modal, Select, Space } from 'antd';
import { ArrowRightOutlined, CheckOutlined, DownOutlined, FileTextOutlined, PlusOutlined, SwapOutlined, StopOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { v4 as uuid } from 'uuid';
import { getLocalizedErrorMessage } from '@/components/request';
import type { WorkflowSession } from '@/modules/chat/store/workflowPanel';
import { SlotRenderer } from './SlotComponents';
import { PRODUCT_STAGES, productApi, type ProductArtifact, type ProductDecision, type ProductDecisionRequest, type ProductRelayRequest, type ProductStage, type ProductSummary } from './productApi';
import './ProductProject.scss';

const labels = ['产品方向', '竞品与生态位', '产品方案', 'PRD', '交互原型', '方案评审', '研发交付'];
// Generated prototypes run in an opaque origin and cannot contact the host or a network.
export function productPreviewHTML(content: string) {
  const doc = new DOMParser().parseFromString(content, 'text/html');
  doc.querySelectorAll('base,meta[http-equiv]').forEach(node => node.remove());
  const policy = doc.createElement('meta');
  policy.httpEquiv = 'Content-Security-Policy';
  policy.content = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; connect-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'";
  doc.head.prepend(policy);
  return `<!doctype html>\n${doc.documentElement.outerHTML}`;
}

export function ProductProject({ session, disabled, beforeAction, onRefresh, onSendMessage }: {
  session: WorkflowSession; disabled: boolean; beforeAction: () => Promise<boolean>;
  onRefresh: () => void; onSendMessage?: (message: string) => void;
}) {
  const { i18n } = useTranslation();
  const zh = !i18n.language?.startsWith('en');
  const text = (cn: string, en: string) => zh ? cn : en;
  const stageLabel = (stage: ProductStage) => zh ? labels[PRODUCT_STAGES.indexOf(stage)] : stage;
  const [summary, setSummary] = useState<ProductSummary>();
  const [revision, setRevision] = useState(0);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [stage, setStage] = useState<ProductStage>('direction');
  const [request, setRequest] = useState('');
  const [switchOpen, setSwitchOpen] = useState(false);
  const [preview, setPreview] = useState<ProductArtifact>();
  const [format, setFormat] = useState('html');
  const [inlinePreview, setInlinePreview] = useState(false);
  const command = useRef<{ key: string; body: ProductRelayRequest }>();
  const decisionCommand = useRef<{ key: string; body: ProductDecisionRequest }>();
  const operation = useRef(0);
  const active = useRef(session.session_id);
  active.current = session.session_id;
  useEffect(() => {
    setSummary(undefined); setPreview(undefined); setInlinePreview(false); setRequest(''); setSwitchOpen(false);
    command.current = undefined; decisionCommand.current = undefined; setBusy(false);
    operation.current++;
    return () => { operation.current++; };
  }, [session.session_id]);
  useEffect(() => {
    const controller = new AbortController();
    productApi.summary(session.session_id, controller.signal).then(value => {
      if (controller.signal.aborted) return;
      setSummary(value); setError('');
    }).catch(cause => { if (!controller.signal.aborted) setError(getLocalizedErrorMessage(cause)); });
    return () => controller.abort();
  }, [session.session_id, session.state_version, revision]);
  const refresh = () => { setRevision(value => value + 1); onRefresh(); };
  async function view(selected: ProductStage, nextFormat = format, inline = false) {
    const op = ++operation.current;
    setBusy(true);
    try {
      const value = await productApi.artifact(session.session_id, selected, nextFormat);
      if (op === operation.current) { setPreview(value); setFormat(nextFormat); setInlinePreview(inline); }
    } catch (cause) { if (op === operation.current) setError(getLocalizedErrorMessage(cause)); }
    finally { if (op === operation.current) setBusy(false); }
  }
  async function relay(action: ProductRelayRequest['action'], selected?: ProductStage, accept = false) {
    if (busy || disabled || !summary || !(await beforeAction())) return;
    const source = session.session_id;
    setBusy(true); setError('');
    try {
      // Read after flushing editor changes. Retain this exact command on transport retries.
      const key = JSON.stringify([source, action, selected, request, accept]);
      if (command.current?.key !== key) {
        const latest = await productApi.summary(source);
        command.current = { key, body: { action, selected_stage: selected, request_context: request,
          expected_state_version: latest.state_version, idempotency_key: uuid(),
          accept_current_artifact: action !== 'finish' && accept } };
      }
      const result = await productApi.relay(source, command.current.body);
      if (active.current !== source) return;
      command.current = undefined; setSwitchOpen(false); refresh();
      if (result.session_id !== source) onSendMessage?.(text(`继续执行已准备的${stageLabel(selected!)}阶段。`, `Continue the prepared ${selected} stage.`));
    } catch (cause) {
      if (active.current === source) {
        setError(getLocalizedErrorMessage(cause));
        // A rejected CAS needs a fresh review; a transport failure keeps its key.
        if ((cause as { response?: { status?: number } }).response?.status === 409) command.current = undefined;
        refresh();
      }
    } finally { if (active.current === source) setBusy(false); }
  }
  async function decide(decision: ProductDecision, action: ProductDecisionRequest['action']) {
    if (busy || disabled || !summary?.project?.can_update_decisions || !(await beforeAction())) return;
    const source = session.session_id;
    setBusy(true); setError('');
    const key = JSON.stringify([source, decision.decision_id, decision.decision_hash, action]);
    if (decisionCommand.current?.key !== key) {
      // Use the version/hash the user actually reviewed. A newer snapshot must
      // be displayed before it can be approved; never silently refresh first.
      decisionCommand.current = { key, body: { action, expected_state_version: summary.state_version,
        expected_decision_hash: decision.decision_hash, idempotency_key: uuid() } };
    }
    try {
      await productApi.decide(source, decision.decision_id, decisionCommand.current.body);
      if (active.current === source) { decisionCommand.current = undefined; command.current = undefined; refresh(); }
    } catch (cause) {
      if (active.current === source) {
        setError(getLocalizedErrorMessage(cause));
        if ((cause as { response?: { status?: number } }).response?.status === 409) decisionCommand.current = undefined;
        refresh();
      }
    } finally { if (active.current === source) setBusy(false); }
  }
  if (!summary?.supported && !error) return null;
  const project = summary?.project;
  const canChange = summary?.can_relay && !disabled && !busy;
  const current = summary?.current_stage;
  const stageInProgress = !summary?.can_relay && (session.status === 'active' || session.status === 'waiting');
  const currentIndex = current ? PRODUCT_STAGES.indexOf(current) : -1;
  const next = summary?.next_stages.find(item => item.id !== current && PRODUCT_STAGES.includes(item.id))?.id;
  const nextAction = next && summary?.actions.includes('continue') && summary.next_stages.some(item => item.id === next)
    ? 'continue' : summary?.actions.includes('switch-stage') ? 'switch-stage' : undefined;
  const currentArtifact = project?.artifacts.find(item => item.stage === current);
  const confirmArtifact = Boolean(summary?.can_accept_current_artifact);
  // Split existing text for presentation only; never invent or persist a project name.
  const projectName = project?.name?.trim() || text('产品项目', 'Product project');
  const headingBreak = projectName.search(/[，,。；;\n]/);
  const projectTitle = headingBreak > 0 ? projectName.slice(0, headingBreak) : projectName;
  const projectDescription = headingBreak > 0 ? projectName.slice(headingBreak + 1).trim() : '';
  const artifactRow = (artifact: ProductArtifact) => <div key={artifact.stage} className="product-project__artifact">
    <FileTextOutlined aria-hidden /><strong>{stageLabel(artifact.stage)}</strong>
    <span className="product-project__version">{artifact.version ? `v${artifact.version.replace(/^v/, '')}` : '—'}</span>
    <span className="product-project__artifact-status">{artifact.stale ? text('需复核', 'Needs review') : artifact.status === 'accepted' ? text('已确认', 'Accepted') : artifact.status === 'reviewable' ? text('待确认', 'Awaiting confirmation') : text('草稿', 'Draft')}</span>
    <Button type="link" disabled={!artifact.available || busy} onClick={() => void view(artifact.stage)}>{text('查看产物', 'View artifact')} ↗</Button>
  </div>;
  const descriptions: Record<ProductStage, [string, string]> = {
    direction: ['明确用户、问题与产品目标。', 'Define users, problems and product goals.'],
    competitive: ['基于当前产品方向，分析竞品并明确差异化定位。', 'Analyze alternatives and define a differentiated position.'],
    design: ['结合产品方向与竞品分析，形成可执行的产品方案。', 'Turn direction and research into a product solution.'],
    prd: ['细化需求、业务规则与验收标准。', 'Detail requirements, business rules and acceptance criteria.'],
    prototype: ['将需求转化为可体验的页面与交互。', 'Turn requirements into an interactive experience.'],
    review: ['评审方案完整性、体验与交付风险。', 'Review completeness, experience and delivery risks.'],
    handoff: ['整理研发所需的方案、规格与交付说明。', 'Prepare specifications and materials for development.'],
  };
  return <section className="product-project" aria-label={text('产品项目', 'Product project')}>
    <header className="product-project__heading">
      <span className="product-project__eyebrow">{text('当前项目', 'Current project')}</span>
      <h3 title={projectName}>{projectTitle}</h3>
      {projectDescription && <p className="product-project__description" title={projectDescription}>{projectDescription}</p>}
    </header>
    {error && <Alert type="error" message={error} showIcon />}
    {currentArtifact && <div className="product-project__artifacts" aria-label={text('当前阶段产物', 'Current stage artifact')}>{artifactRow(currentArtifact)}</div>}
    {currentIndex >= 0 && <ul className="product-project__stages" aria-label={text('产品阶段', 'Product stages')}>
      {PRODUCT_STAGES.map((id, index) => {
        const artifact = project?.artifacts.find(item => item.stage === id);
        // Publication history and freshness are separate: a completed view may need review.
        const done = artifact?.available && /^v?\d+\.\d+$/.test(artifact.version);
        const selected = id === (inlinePreview && preview ? preview.stage : current);
        const status = done ? text('已完成', 'Completed') : artifact?.available ? text('草稿', 'Draft') : text('暂无成果', 'No artifact');
        return <li key={id} className={[selected ? 'is-current' : '', done ? 'is-complete' : ''].join(' ')}>
          <button type="button" className="product-project__stage-button" disabled={busy || !artifact?.available}
            aria-label={`${stageLabel(id)} · ${status}${artifact?.stale ? text(' · 需复核', ' · Needs review') : ''}`}
            aria-pressed={selected} title={artifact?.stale ? text('已完成，成果需复核', 'Completed; artifact needs review') : status}
            onClick={() => void view(id, format, true)}>
            <span className="product-project__stage-number">{done ? <CheckOutlined aria-hidden /> : index + 1}</span>
            <span>{stageLabel(id)}</span>
            {artifact?.stale && <small>{text('需复核', 'Needs review')}</small>}
          </button>
        </li>;
      })}
    </ul>}
    {preview && inlinePreview && <section className="product-project__selected-artifact" aria-label={text('阶段成果', 'Stage artifact')}>
      <header><h4>{stageLabel(preview.stage)}{text('成果', ' artifact')}</h4>
        <Button type="text" onClick={() => { operation.current++; setPreview(undefined); setInlinePreview(false); setBusy(false); }}>{text('返回当前工作', 'Back to current work')}</Button>
      </header>
      {(project?.artifacts.find(item => item.stage === preview.stage)?.stale || preview.stale) && <p role="status">{text('此阶段已产生成果，当前版本需复核。', 'This stage has an artifact; its current version needs review.')}</p>}
      <Space><Button disabled={busy} onClick={() => void view(preview.stage, 'html', true)}>HTML</Button><Button disabled={busy} onClick={() => void view(preview.stage, 'markdown', true)}>Markdown</Button></Space>
      {preview.content_format === 'html'
        ? <iframe className="product-project__preview" sandbox="allow-scripts" title={preview.title} srcDoc={productPreviewHTML(preview.content || '')} />
        : <SlotRenderer readOnly slot={{ slot_id: preview.slot_id, slot: preview.slot_id, revision: preview.revision,
          selected: false, created_at: '', content_type: 'text', artifact_value: { text: preview.content } }} />}
    </section>}
    {!!project?.drafts?.length && <p className="product-project__notice" role="status">{text('存在未发布修改。预览和下一阶段仍使用上一份完整交付；修订并发布后才会更新。', 'Unpublished changes exist. Previews and subsequent stages use the last complete delivery until revisions are published.')}</p>}
    {!!project?.decisions?.length && <section className="product-project__decisions" aria-label={text('产品决定', 'Product decisions')}>
      <h4>{text('产品决定', 'Product decisions')}</h4>
      <p>{text('请逐项查看并确认或暂缓。暂缓保留草稿；结束或修订阶段不会自动确认任何决定。', 'Review and accept or defer each decision. Deferring keeps a draft; finishing or revising a stage never accepts decisions.')}</p>
      {project.decisions.map(decision => <article key={decision.decision_id}>
        <strong>{decision.title || decision.question || decision.decision_id}</strong>
        <p>{decision.statement || decision.decision || decision.question}</p>
        {decision.rationale && <p>{decision.rationale}</p>}
        {decision.decision_content && <details><summary>{text('查看完整决定内容', 'View complete decision')}</summary>
          <pre>{JSON.stringify(decision.decision_content, null, 2)}</pre></details>}
        <p>{decision.status === 'accepted' ? text('已确认', 'Accepted') : decision.deferred ? text('已暂缓，仍为草稿', 'Deferred, remains a draft') : text('待确认', 'Awaiting confirmation')}
          {decision.confirmation_required && decision.status !== 'accepted' && !decision.deferred && text(' · 进入下一阶段前须确认或暂缓', ' · Accept or defer before entering another stage')}</p>
        {decision.status !== 'accepted' && <Space>
          <Button disabled={busy || disabled || !project.can_update_decisions} onClick={() => void decide(decision, 'accept')}>{text('确认此决定', 'Accept decision')}</Button>
          <Button disabled={busy || disabled || !project.can_update_decisions || decision.deferred} onClick={() => void decide(decision, 'defer')}>{text('暂缓此决定', 'Defer decision')}</Button>
        </Space>}
      </article>)}
    </section>}
    {summary?.can_relay && <>
      <div className="product-project__recommendation">
        <div className="product-project__recommendation-heading">
          <span className="product-project__eyebrow">{next ? text('推荐下一阶段', 'Recommended next stage') : text('选择接下来要做的内容', 'Choose what to work on next')}</span>
          {current && next && <span className="product-project__transition">{stageLabel(current)} <ArrowRightOutlined aria-hidden /> {stageLabel(next)}</span>}
        </div>
        <h4>{next ? stageLabel(next) : text('按目标选择阶段', 'Choose a stage for your goal')}</h4>
        <p>{next ? text(...descriptions[next]) : text('查看交付成果，或根据反馈继续修订。', 'Review the deliverables or revise them based on feedback.')}</p>
      </div>
      <details className="product-project__request">
        <summary><PlusOutlined aria-hidden /> {text('添加补充要求', 'Add requirements')} <small>{text('选填', 'Optional')}</small></summary>
        <Input.TextArea rows={3} maxLength={8000} value={request} disabled={busy} onChange={(event: ChangeEvent<HTMLTextAreaElement>) => setRequest(event.target.value)}
          aria-label={text('补充要求', 'Additional requirements')} placeholder={text('告诉我下一阶段或本次修订需要重点关注什么…', 'What should the next stage or revision focus on?')} />
      </details>
      <footer className="product-project__footer">
        {next && confirmArtifact && <p>{text('继续将确认当前产物。', 'Continuing accepts the current artifact.')}</p>}
        <div className="product-project__actions">
          {next ? <Button type="primary" disabled={!canChange || !nextAction} onClick={() => nextAction && void relay(nextAction, next, confirmArtifact)}>
            {confirmArtifact ? text('确认产物并进入', 'Accept and enter ') : text('进入', 'Enter ')}{stageLabel(next)} <ArrowRightOutlined aria-hidden />
          </Button> : !next && currentArtifact && <Button type="primary" disabled={!currentArtifact.available || busy} onClick={() => void view(currentArtifact.stage)}>{text('查看交付成果', 'View deliverables')} <ArrowRightOutlined aria-hidden /></Button>}
          {current && <Button disabled={!canChange || !summary.actions.includes('switch-stage')} onClick={() => void relay('switch-stage', current)}>{text('修订当前阶段', 'Revise current stage')}</Button>}
          <Button disabled={!canChange || !summary.actions.includes('switch-stage')} onClick={() => { setStage(current ?? 'direction'); setSwitchOpen(true); }}>{text('选择其他阶段', 'Choose another stage')} <SwapOutlined aria-hidden /></Button>
          <Dropdown trigger={['click']} menu={{ items: [
            ...(summary.actions.includes('finish') ? [{ key: 'finish', icon: <StopOutlined />, label: text('结束本轮工作流', 'Finish this workflow'), onClick: () => void relay('finish') }] : []),
          ] }} disabled={!canChange}>
            <Button type="text" className="product-project__more" disabled={!canChange}>{text('更多操作', 'More actions')} <DownOutlined aria-hidden /></Button>
          </Dropdown>
        </div>
      </footer>
    </>}
    {stageInProgress && current && <p className="product-project__progress" role="status">
      {session.status === 'waiting'
        ? text(`${stageLabel(current)}正在等待当前步骤处理，完成后即可查看阶段产物。`, `${stageLabel(current)} is waiting for the current step. The artifact will appear when it is complete.`)
        : text(`${stageLabel(current)}正在执行，完成后即可查看阶段产物。`, `${stageLabel(current)} is in progress. The artifact will appear when it is complete.`)}
    </p>}
    {summary?.reason && !stageInProgress && <p className="product-project__notice" role="status">{summary.reason}</p>}
    <Modal title={text('切换其他阶段', 'Switch stage')} open={switchOpen} onCancel={() => setSwitchOpen(false)}
      onOk={() => void relay('switch-stage', stage)} okText={text('进入所选阶段', 'Enter selected stage')} confirmLoading={busy} okButtonProps={{ disabled: !canChange }}>
      <p>{text('保留当前项目产物，进入所选阶段；不会自动接受当前产物。', 'Keep the project artifacts and enter the selected stage without automatically accepting the current artifact.')}</p>
      <Select aria-label={text('目标阶段', 'Target stage')} value={stage} disabled={!canChange} onChange={setStage} style={{ width: '100%' }} options={PRODUCT_STAGES.map(id => ({ value: id, label: stageLabel(id) }))} />
    </Modal>
    <Modal title={preview?.title} open={!!preview && !inlinePreview} width="90vw" footer={null} onCancel={() => { operation.current++; setPreview(undefined); setBusy(false); }} destroyOnHidden>
      {preview && <>
        <Space><Button disabled={busy} onClick={() => void view(preview.stage, 'html')}>HTML</Button><Button disabled={busy} onClick={() => void view(preview.stage, 'markdown')}>Markdown</Button></Space>
        {preview.content_format === 'html'
          ? <iframe className="product-project__preview" sandbox="allow-scripts" title={preview.title} srcDoc={productPreviewHTML(preview.content || '')} />
          : <SlotRenderer readOnly slot={{ slot_id: preview.slot_id, slot: preview.slot_id, revision: preview.revision,
            selected: false, created_at: '', content_type: 'text', artifact_value: { text: preview.content } }} />}
      </>}
    </Modal>
  </section>;
}
