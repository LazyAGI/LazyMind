import { useEffect, useState } from 'react';
import { Alert, Checkbox, Modal, Progress, Space, Typography } from 'antd';
import { getLocalizedErrorMessage } from '@/components/request';
import type { PdfIRTarget } from '../api/pdfIR';
import { getReadingPreparation, prepareReading, type ReadingPreparation, type ReadingScope } from '../api/readingPreparation';
const labels = { not_requested: '尚未准备', pending: '等待准备', running: '正在准备', succeeded: '准备完成', failed: '准备失败', canceled: '已取消' };
export default function ReadingPreparationAction({ target }: { target: PdfIRTarget }) {
  const [open, setOpen] = useState(false);
  const [scopes, setScopes] = useState<ReadingScope[]>(['pdf_parse']);
  const [state, setState] = useState<ReadingPreparation>();
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    if (!open) return;
    let stale = false; let timer: ReturnType<typeof setTimeout>;
    setState(undefined); setError('');
    const poll = async () => {
      try { const result = await getReadingPreparation(target.dataset, target.document); if (stale) return; setState(result);
        if (result.status === 'pending' || result.status === 'running') timer = setTimeout(poll, 1500);
      } catch (err) { if (!stale) setError(getLocalizedErrorMessage(err)); }
    };
    void poll(); return () => { stale = true; clearTimeout(timer); };
  }, [open, target.dataset, target.document, revision]);
  const running = state?.status === 'pending' || state?.status === 'running';
  return <><button type="button" onClick={() => setOpen(true)}>准备当前论文</button>
    <Modal title="准备当前论文" open={open} onCancel={() => { if (!submitting) setOpen(false); }} closable={!submitting} maskClosable={!submitting} keyboard={!submitting} cancelButtonProps={{ disabled: submitting }} okText="开始准备" cancelText="关闭" confirmLoading={submitting} okButtonProps={{ disabled: !state || !scopes.length || running || target.canWrite === false }} onOk={async () => {
      setSubmitting(true); setError('');
      try { setState(await prepareReading(target.dataset, target.document, scopes)); setRevision(v => v + 1); }
      catch (err) { setError(getLocalizedErrorMessage(err)); } finally { setSubmitting(false); }
    }}>
      <Typography.Paragraph>仅准备当前论文，已有结果会复用。解析和研读可能产生费用，禁用的能力无法生成；每篇研读总数沿用个人预设限额。</Typography.Paragraph>
      <Checkbox.Group aria-label="准备范围" value={scopes} disabled={running || submitting} onChange={values => setScopes(values as ReadingScope[])}><Space wrap>
        <Checkbox value="pdf_parse">结构解析</Checkbox><Checkbox value="formula">公式研读</Checkbox><Checkbox value="figure">图片研读</Checkbox><Checkbox value="table">表格研读</Checkbox>
      </Space></Checkbox.Group>
      <Typography.Paragraph type="secondary">公式、图片和表格研读需要论文结构；缺少时会先解析。文档对话请在对话面板点击「准备文档」。</Typography.Paragraph>
      {target.canWrite === false && <Alert type="info" message="当前论文为只读，无法提交准备。" />}
      {error && <Alert type="error" showIcon message={error} />}
      {state && <div role="status"><Typography.Paragraph>{labels[state.status]}</Typography.Paragraph>
        {!!state.progress_total && <Progress percent={Math.round((state.progress_current || 0) / state.progress_total * 100)} status={state.status === 'failed' ? 'exception' : state.status === 'succeeded' ? 'success' : 'active'} />}
        {state.status !== 'not_requested' && <Typography.Paragraph type="secondary">已完成 {state.progress_current || 0} / {state.progress_total || 0}；模型请求 {state.model_requests || 0} 次（含失败与备用调用，不代表费用金额）</Typography.Paragraph>}
        {state.error_message && <Alert type="error" message={state.error_message} />}
      </div>}
    </Modal>
  </>;
}
