import { useEffect, useState } from 'react';
import { ThunderboltOutlined } from '@ant-design/icons';
import { Alert, Button, Collapse, InputNumber, Modal, Select, Space, Spin, Typography, theme } from 'antd';
import { getLocalizedErrorMessage } from '@/components/request';
import { getReadingPreferences, saveReadingPreferences, type ReadingPreferences, type ReadingCapabilityId, type ReadingMode } from '../api/readingPreparation';
import './ReadingPreferencesModal.css';
const names: Record<ReadingCapabilityId, string> = { pdf_parse: 'PDF 解析', ai_study: 'AI 论文精读' };
export default function ReadingPreferencesModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { token } = theme.useToken();
  const [draft, setDraft] = useState<ReadingPreferences>();
  const [loadedRefs, setLoadedRefs] = useState({ parser_ref: '', study_model_ref: '' });
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [reload, setReload] = useState(0);
  useEffect(() => {
    if (!open) return;
    let stale = false; setLoading(true); setError(''); setDraft(undefined);
    getReadingPreferences().then(value => { if (!stale) { setDraft(value); setLoadedRefs({ parser_ref: value.parser_ref || '', study_model_ref: value.study_model_ref || '' }); } }).catch(err => { if (!stale) setError(getLocalizedErrorMessage(err)); }).finally(() => { if (!stale) setLoading(false); });
    return () => { stale = true; };
  }, [open, reload]);
  const needsStructureHint = draft?.modes.ai_study === 'automatic' && draft.modes.pdf_parse !== 'automatic';
  return <Modal title={<div className="reading-preferences-title"><span className="reading-preferences-emblem"><ThunderboltOutlined /></span><div>AI 阅读准备<span>管理论文解析与 AI 研读的准备方式</span></div></div>} open={open} onCancel={() => { if (!saving) onClose(); }} closable={!saving} maskClosable={!saving} keyboard={!saving} width={720} centered className="reading-preferences" footer={<Space><Button disabled={saving} onClick={onClose}>取消</Button><Button type="primary" loading={saving} disabled={!draft || loading} onClick={async () => {
    if (!draft) return; setSaving(true); setError('');
    try { await saveReadingPreferences({ ...draft, preset: 'custom' }); onClose(); } catch (err) { setError(getLocalizedErrorMessage(err)); } finally { setSaving(false); }
  }}>保存设置</Button></Space>}>

    {error && <Alert type="error" showIcon message={error} action={!draft ? <Button onClick={() => setReload(v => v + 1)}>重试</Button> : undefined} />}
    {loading && <Spin />}
    {draft && <div style={{ '--reading-border': token.colorBorderSecondary, '--reading-muted': token.colorTextSecondary, '--reading-surface': token.colorFillQuaternary } as React.CSSProperties}>
      <div className="reading-section-heading reading-model-heading"><span>服务与模型</span><span className="reading-section-hint">解析服务与知识库共用</span></div>
      <div className="reading-provider-selections">
        {([['parser', 'parser_ref', '文档解析服务', '/settings?section=model_defaults&service=parsing'], ['study', 'study_model_ref', '研读模型', '/settings?section=models']] as const).map(([kind, field, label, href]) => {
          const metadata = draft.provider_selections?.[kind];
          const selected = draft[field] || '';
          const missing = selected && !metadata?.options.some(option => option.id === selected);
          return <div className="reading-capability" key={kind}><div><strong>{label}</strong> <a href={href} aria-label={`配置${label}`}>去配置</a>
            {metadata?.unavailable_reason && selected === loadedRefs[field] && <p><Typography.Text type="warning">{metadata.unavailable_reason}</Typography.Text></p>}</div>
            <Select aria-label={label} disabled={saving} value={selected} onChange={(value: string) => setDraft({ ...draft, [field]: value })} options={[
              { value: '', label: `跟随默认${metadata?.default_label ? `（${metadata.default_label}）` : ''}` },
              ...(metadata?.options || []).map(option => ({ value: option.id, label: option.label })),
              ...(missing ? [{ value: selected, label: '已选服务不可用', disabled: true }] : []),
            ]} />
          </div>;
        })}
        <Typography.Paragraph type="secondary">精读模型独立于聊天模型。切换服务或模型后会继续保留已有结果。</Typography.Paragraph>
      </div>
      <div className="reading-section-heading"><span>准备方式</span><span className="reading-section-hint">已有结果优先复用</span></div>
      <div className="reading-capabilities">
        {Object.entries(names).map(([key, name]) => {
          const id = key as ReadingCapabilityId; const capability = draft.capabilities.find(c => c.id === id);
          return <div className="reading-capability" key={id}><div><strong>{name}</strong><p>{id === 'pdf_parse' ? '识别正文、公式、图片与表格，供后续研读使用。' : '统一精读论文中的公式、图片和表格。'}</p>
            {capability?.unavailable_reason && <Typography.Text type="warning">{capability.unavailable_reason}</Typography.Text>}</div>
            <Select aria-label={name} value={draft.modes[id]} disabled={saving} options={[{ value: 'on_demand', label: '按需' }, { value: 'automatic', label: '自动' }, { value: 'disabled', label: '禁用生成' }]} onChange={(mode: ReadingMode) => setDraft({ ...draft, modes: { ...draft.modes, [id]: mode } })} />
          </div>;
        })}
      </div>
      {needsStructureHint && <Alert type="info" showIcon message="自动精读优先使用已有结构；缺少结构时需开启自动解析或手动解析。" />}
      <div className="reading-quota-summary" aria-label="今日自动额度">
        <div className="reading-section-heading"><span>今日自动额度</span><span className="reading-section-hint">已用 / 上限 · UTC 日期</span></div>
        {(['pdf_parse', 'ai_study'] as const).map(id => {
          const quota = draft.usage?.[id];
          return quota && <div className="reading-quota-row" key={id}>
            <span>{names[id]}</span><span>{quota.used} / {quota.limit} 篇</span>
          </div>;
        })}
      </div>
      {(['pdf_parse', 'ai_study'] as const).map(id => {
        const quota = draft.usage?.[id];
        return quota && quota.used >= quota.limit && <Alert key={id} type="warning" showIcon message={`${names[id]}额度已用尽，待处理的自动任务将等待次日额度。`} />;
      })}
      <Collapse ghost items={[{ key: 'limits', label: '调整每日自动额度', children: <div className="reading-limits">
        {([['pdf_parse', 'daily_pdf_documents'], ['ai_study', 'daily_study_documents']] as const).map(([id, field]) => <label key={id}>{names[id]}每日额度<InputNumber aria-label={`${names[id]}每日额度`} disabled={saving} min={1} max={10000} precision={0} value={draft.limits[field]} onChange={(value: number | null) => value != null && setDraft({ ...draft, limits: { ...draft.limits, [field]: value } })} />篇</label>)}
        <Typography.Text type="secondary">两类额度分别按论文计数。缓存复用和按需准备不占自动额度。自动任务串行执行（并发 1）。</Typography.Text>
      </div> }]} />
      <Typography.Paragraph className="reading-cost-note" type="secondary">自动准备覆盖论文集中已有和新加入且已取得 PDF 的论文，按每日额度处理，可能产生费用。失败任务需手动重试。禁用后保留已有结果，已发出的请求可能继续计费。</Typography.Paragraph>
    </div>}
  </Modal>;
}
