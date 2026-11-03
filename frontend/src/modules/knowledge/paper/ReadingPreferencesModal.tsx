import { useEffect, useState } from 'react';
import { CheckOutlined, ThunderboltOutlined, SlidersOutlined } from '@ant-design/icons';
import { Alert, Button, Collapse, InputNumber, Modal, Radio, Select, Space, Spin, Typography, theme } from 'antd';
import { getLocalizedErrorMessage } from '@/components/request';
import { getReadingPreferences, saveReadingPreferences, presetModes, readingDependencyError, type ReadingPreferences, type ReadingPreset, type ReadingCapabilityId } from '../api/readingPreparation';
import './ReadingPreferencesModal.css';
const names: Record<ReadingCapabilityId, string> = { pdf_parse: '结构解析', chat_prepare: '文档对话准备', formula: '公式研读', figure: '图片研读', table: '表格研读' };
const presets = [ ['demand', '按需准备', '用到时再生成', '由你决定每一次 AI 调用'], ['advance', '提前准备', '先解析，后研读', '导入后解析，解读按需生成'], ['full', '充分准备', '让解读提前就绪', '自动解析并生成公式、图表解读'] ] as const;
export default function ReadingPreferencesModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { token } = theme.useToken();
  const [draft, setDraft] = useState<ReadingPreferences>();
  const [loadedRefs, setLoadedRefs] = useState({ parser_ref: '', study_model_ref: '' });
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [reload, setReload] = useState(0);
  const [detailsOpen, setDetailsOpen] = useState(false);
  useEffect(() => {
    if (!open) return;
    let stale = false; setDetailsOpen(false); setLoading(true); setError(''); setDraft(undefined);
    getReadingPreferences().then(value => { if (!stale) { setDraft(value); setLoadedRefs({ parser_ref: value.parser_ref || '', study_model_ref: value.study_model_ref || '' }); } }).catch(err => { if (!stale) setError(getLocalizedErrorMessage(err)); }).finally(() => { if (!stale) setLoading(false); });
    return () => { stale = true; };
  }, [open, reload]);
  const dependencyError = draft && readingDependencyError(draft);
  return <Modal title={<div className="reading-preferences-title"><span className="reading-preferences-emblem"><ThunderboltOutlined /></span><div>AI 阅读准备<span>让准备节奏，适合你的阅读习惯</span></div></div>} open={open} onCancel={() => { if (!saving) onClose(); }} closable={!saving} maskClosable={!saving} keyboard={!saving} width={720} centered className="reading-preferences" footer={<Space><Button disabled={saving} onClick={onClose}>取消</Button><Button type="primary" loading={saving} disabled={!draft || loading || !!dependencyError} onClick={async () => {
    if (!draft) return; setSaving(true); setError('');
    try { await saveReadingPreferences(draft); onClose(); } catch (err) { setError(getLocalizedErrorMessage(err)); } finally { setSaving(false); }
  }}>保存预设</Button></Space>}>

    {error && <Alert type="error" showIcon message={error} action={!draft ? <Button onClick={() => setReload(v => v + 1)}>重试</Button> : undefined} />}
    {loading && <Spin />}
    {draft && <div style={{ '--reading-border': token.colorBorderSecondary, '--reading-muted': token.colorTextSecondary, '--reading-primary': token.colorPrimary, '--reading-tint': token.colorPrimaryBg, '--reading-surface': token.colorFillQuaternary } as React.CSSProperties}>
      <div className="reading-section-heading"><span>准备方案</span><span className="reading-section-hint">已有结果始终优先复用</span></div>
      <Radio.Group aria-label="准备预设" className="reading-preset-options" disabled={saving} value={draft.preset} onChange={event => {
        const preset = event.target.value as ReadingPreset;
        setDetailsOpen(false);
        setDraft({ ...draft, preset, modes: preset === 'custom' ? draft.modes : presetModes(preset) });
      }}>
        {presets.map(([value, title, subtitle, description]) => <Radio key={value} value={value}><span className="reading-preset-heading"><strong>{title}</strong><CheckOutlined /></span><span className="reading-preset-subtitle">{subtitle}</span><span className="reading-preset-description">{description}</span></Radio>)}
      </Radio.Group>
      <div className="reading-plan-summary"><span>{draft.preset === 'custom' ? `自定义 · ${Object.entries(names).filter(([id]) => draft.modes[id as ReadingCapabilityId] === 'automatic').map(([, name]) => name).join('、') || '无自动准备'}${Object.values(draft.modes).some(mode => mode === 'automatic') ? '自动准备' : ''}` : '仅对之后导入的论文自动准备，不补跑历史论文'}</span><Button type="link" size="small" disabled={saving} icon={<SlidersOutlined />} aria-expanded={detailsOpen} onClick={() => setDetailsOpen(!detailsOpen)}>{detailsOpen ? '收起设置' : '逐项调整'}</Button></div>
      {(detailsOpen) && <div className="reading-capabilities">
        {Object.entries(names).map(([key, name]) => {
          const id = key as ReadingCapabilityId; const capability = draft.capabilities.find(c => c.id === id);
          return <div className="reading-capability" key={id}><div><strong>{name}</strong><p>{id === 'chat_prepare' ? '为文档对话准备可检索文本，与结构解析独立。' : id === 'pdf_parse' ? '识别正文、公式、图片与表格，供后续研读使用。' : '每个条目可能调用模型，已有结果优先复用。'}</p>
            {capability?.unavailable_reason && <Typography.Text type="warning">{capability.unavailable_reason}</Typography.Text>}</div>
            <Select aria-label={name} value={draft.modes[id]} disabled={saving} options={[{ value: 'on_demand', label: '按需' }, { value: 'automatic', label: '自动' }, { value: 'disabled', label: '禁用生成' }]} onChange={mode => setDraft({ ...draft, preset: 'custom', modes: { ...draft.modes, [id]: mode } })} />
          </div>;
        })}
        <div className="reading-unavailable">论文速览 <span>暂不可用</span></div>
      </div>}
      {!(detailsOpen) && draft.capabilities.filter(c => c.id !== 'overview' && c.unavailable_reason).map(c => <Alert key={c.id} type="warning" showIcon message={c.unavailable_reason} />)}
      <div className="reading-section-heading reading-model-heading"><span>服务与模型</span><span className="reading-section-hint">仅用于论文阅读</span></div>
      <div className="reading-provider-selections">
        {([['parser', 'parser_ref', '文档解析服务', '/settings?section=model_defaults&service=parsing'], ['study', 'study_model_ref', '研读模型', '/settings?section=models']] as const).map(([kind, field, label, href]) => {
          const metadata = draft.provider_selections?.[kind];
          const selected = draft[field] || '';
          const missing = selected && !metadata?.options.some(option => option.id === selected);
          return <div className="reading-capability" key={kind}><div><strong>{label}</strong> <a href={href} aria-label={`配置${label}`}>去配置</a>
            {metadata?.unavailable_reason && selected === loadedRefs[field] && <p><Typography.Text type="warning">{metadata.unavailable_reason}</Typography.Text></p>}</div>
            <Select aria-label={label} disabled={saving} value={selected} onChange={value => setDraft({ ...draft, [field]: value })} options={[
              { value: '', label: `跟随默认${metadata?.default_label ? `（${metadata.default_label}）` : ''}` },
              ...(metadata?.options || []).map(option => ({ value: option.id, label: option.label })),
              ...(missing ? [{ value: selected, label: '已选服务不可用', disabled: true }] : []),
            ]} />
          </div>;
        })}
        <Typography.Paragraph type="secondary">独立于聊天与全局默认，切换不会重新生成已有解读。</Typography.Paragraph>
      </div>
      {dependencyError && <Alert type="warning" showIcon message={dependencyError} />}
      <Collapse ghost items={[{ key: 'limits', label: <span className="reading-limit-heading"><span>自动准备上限</span><span>每天 {draft.limits.daily_documents} 篇 · 每篇 {draft.limits.study_items_per_document} 项</span></span>, children: <div className="reading-limits">
        <label>每天自动准备论文<InputNumber aria-label="每天自动准备论文" disabled={saving} min={1} max={100} value={draft.limits.daily_documents} onChange={value => value != null && setDraft({ ...draft, limits: { ...draft.limits, daily_documents: value } })} />篇</label>
        <label>每篇研读条目总数<InputNumber aria-label="每篇研读条目总数" disabled={saving} min={1} max={100} value={draft.limits.study_items_per_document} onChange={value => value != null && setDraft({ ...draft, limits: { ...draft.limits, study_items_per_document: value } })} />项</label>
        <Typography.Text type="secondary">每日额度按 UTC 日期计算。公式、图片、表格合计计数；自动任务串行执行（并发 1）。次数限制不是费用金额上限。</Typography.Text>
      </div> }]} />
      <Typography.Paragraph className="reading-cost-note" type="secondary">自动准备可能产生费用。禁用后保留已有结果，已发出的请求可能继续计费。</Typography.Paragraph>
    </div>}
  </Modal>;
}
