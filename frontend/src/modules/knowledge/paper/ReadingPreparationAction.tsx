import { StudySparkle } from "@/components/ui/PdfStudyActivation";
import PaperPreparationStatus from './PaperPreparationStatus';
import { useEffect, useRef, useState } from 'react';
import { Alert, Checkbox, Modal, Progress, Space, Typography } from 'antd';
import { getLocalizedErrorMessage } from '@/components/request';
import type { PdfIRTarget } from '../api/pdfIR';
import { getReadingPreparation, prepareReading, readingScopeLabels, type ReadingPreparation, type ReadingScope } from '../api/readingPreparation';
function waitingReason(state?: ReadingPreparation): string | undefined {
  if (state?.status !== 'pending') return;
  if (state.error_code === 'reading_quota_exhausted') return '等待自动额度';
  if (state.error_code === 'reading_structure_required') return '等待 PDF 解析';
}
const active = (state?: ReadingPreparation) => state?.status === 'running' || state?.status === 'pending' && !waitingReason(state);
const labels = { not_requested: '尚未准备', pending: '等待准备', running: '正在准备', succeeded: '准备完成', failed: '准备失败', canceled: '已取消' };
export default function ReadingPreparationAction({ target, onPrepared }: { target: PdfIRTarget; onPrepared?: () => void }) {
  const targetKey = `${target.dataset}/${target.document}`;
  const currentTarget = useRef(targetKey);
  currentTarget.current = targetKey;
  const preparedCallback = useRef(onPrepared);
  preparedCallback.current = onPrepared;
  const activeJob = useRef<string>();
  const notifiedJobs = useRef(new Set<string>());
  const [open, setOpen] = useState(false);
  const [scopes, setScopes] = useState<ReadingScope[]>(['pdf_parse']);
  const [state, setState] = useState<ReadingPreparation>();
  const [error, setError] = useState('');
  const submission = useRef<string>();
  const [submitting, setSubmitting] = useState(false);
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    if (activeJob.current && !activeJob.current.startsWith(`${targetKey}/`)) activeJob.current = undefined;
    // Keep observing a known active batch after the dialog closes so the reader
    // receives its new artifacts. Detailed parse/chat polling remains dialog-only.
    if (!open && !activeJob.current) return;
    let stale = false; let timer: ReturnType<typeof setTimeout>;
    setState(undefined); setError('');
    const poll = async () => {
      try { const result = await getReadingPreparation(target.dataset, target.document); if (stale || currentTarget.current !== targetKey) return; setState(result); setError('');
        const jobKey = result.job_id ? `${targetKey}/${result.job_id}` : undefined;
        if (jobKey && active(result)) activeJob.current = jobKey;
        if (jobKey && result.status === 'succeeded' && activeJob.current === jobKey && !notifiedJobs.current.has(jobKey)) {
          notifiedJobs.current.add(jobKey);
          preparedCallback.current?.();
        }
        if (!active(result)) activeJob.current = undefined;
        if (open || activeJob.current) timer = setTimeout(poll, active(result) ? 1500 : 4000);
      } catch (err) { if (!stale) { setError(getLocalizedErrorMessage(err)); if (open || activeJob.current) timer = setTimeout(poll, 4000); } }
    };
    void poll(); return () => { stale = true; clearTimeout(timer); };
  }, [open, target.dataset, target.document, revision]);
  const running = active(state);
  return <><button className="paper-preparation-trigger pdf-study-ai-button" type="button" aria-label="研读论文" title="研读论文 · 查看解析与研读准备进度" onClick={() => setOpen(true)}><StudySparkle/><span>研读论文</span></button>
    <Modal forceRender className="paper-preparation-modal" width={720} centered title="当前论文 · 准备进度" open={open} onCancel={() => { if (!submitting) setOpen(false); }} closable={!submitting} maskClosable={!submitting} keyboard={!submitting} cancelButtonProps={{ disabled: submitting }} okText="开始准备" cancelText="关闭" confirmLoading={submitting} okButtonProps={{ disabled: !state || !scopes.length || running || target.canWrite === false }} onOk={async () => {
      if (!state || !scopes.length || running || target.canWrite === false || submission.current) return;
      submission.current = targetKey;
      setSubmitting(true); setError('');
      try {
        const result = await prepareReading(target.dataset, target.document, scopes);
        if (currentTarget.current !== targetKey) return;
        if (result.job_id) activeJob.current = `${targetKey}/${result.job_id}`;
        setState(result); setRevision(v => v + 1);
      }
      catch (err) { if (currentTarget.current === targetKey) setError(getLocalizedErrorMessage(err)); } finally { submission.current = undefined; setSubmitting(false); }
    }}>
      {/* forceRender lets this guard update even while rc-dialog memoizes closed content. */}
      {open && <PaperPreparationStatus key={targetKey} target={target} capabilities={state?.capabilities} batchStatus={state?.status} onRefresh={() => setRevision(v => v + 1)} />}
      {target.canWrite === false && <Alert type="info" message="当前论文为只读，无法提交准备。" />}
      {error && <Alert type="error" showIcon message={error} />}
      {state && <div className="paper-preparation-batch" role="status"><Typography.Paragraph><strong>最近一次批量准备</strong> · {waitingReason(state) || labels[state.status]}</Typography.Paragraph><Typography.Paragraph type="secondary">{readingScopeLabels(state.scopes).join("、") || "尚无批量准备记录；单独解析的进度见上方"}</Typography.Paragraph>
        {!!state.progress_total && <Progress percent={Math.round((state.progress_current || 0) / state.progress_total * 100)} status={state.status === 'failed' ? 'exception' : state.status === 'succeeded' ? 'success' : 'active'} />}
        {state.status !== 'not_requested' && <Typography.Paragraph type="secondary">任务步骤 {state.progress_current || 0} / {state.progress_total || 0}；模型请求 {state.model_requests || 0} 次（含失败与备用调用，不代表费用金额）</Typography.Paragraph>}
        {state.error_message && <Alert type={waitingReason(state) ? "warning" : "error"} message={state.error_message} />}
      </div>}
      <section className="paper-preparation-options" aria-label="选择本篇准备内容"><Typography.Title level={5}>选择本篇准备内容</Typography.Title>      <Typography.Paragraph>仅准备当前论文，已有结果会复用。解析和研读可能产生费用，禁用的能力无法生成；手动准备不占每日自动额度。</Typography.Paragraph>
      <Checkbox.Group aria-label="准备范围" value={scopes} disabled={running || submitting} onChange={(values: (string | number | boolean)[]) => setScopes(values as ReadingScope[])}><Space wrap>
        <Checkbox value="pdf_parse">PDF 解析</Checkbox><Checkbox value="ai_study">AI 论文精读</Checkbox>
      </Space></Checkbox.Group>
      <Typography.Paragraph type="secondary">公式、图片和表格研读需要论文结构；缺少时会先解析。文档对话请在对话面板点击「准备文档」。</Typography.Paragraph>
</section>
    </Modal>
  </>;
}
