import { useEffect, useState } from 'react';
import { Alert, Button, Collapse, InputNumber, Modal, Radio, Select, Space, Spin, Typography, theme } from 'antd';
import { getLocalizedErrorMessage } from '@/components/request';
import { getReadingPreferences, saveReadingPreferences, presetModes, readingDependencyError, type ReadingPreferences, type ReadingPreset, type ReadingCapabilityId } from '../api/readingPreparation';
import './ReadingPreferencesModal.css';
const names: Record<ReadingCapabilityId, string> = { pdf_parse: '结构解析', chat_prepare: '文档对话准备', formula: '公式研读', figure: '图片研读', table: '表格研读' };
const presets = [ ['demand', '按需准备', '点击后才准备，避免自动开销'], ['advance', '提前准备', '新论文自动解析结构，研读时再生成'], ['full', '充分准备', '自动解析并研读公式、图片和表格'] ] as const;
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
  const dependencyError = draft && readingDependencyError(draft);
  return <Modal title="AI 阅读准备" open={open} onCancel={() => { if (!saving) onClose(); }} closable={!saving} maskClosable={!saving} keyboard={!saving} width={760} centered className="reading-preferences" footer={<Space><Button disabled={saving} onClick={onClose}>取消</Button><Button type="primary" loading={saving} disabled={!draft || loading || !!dependencyError} onClick={async () => {
    if (!draft) return; setSaving(true); setError('');
    try { await saveReadingPreferences(draft); onClose(); } catch (err) { setError(getLocalizedErrorMessage(err)); } finally { setSaving(false); }
  }}>保存预设</Button></Space>}>
    <Typography.Paragraph type="secondary">自动准备仅对我之后导入的论文生效，历史论文不会批量准备。禁用生成立即阻止新的调用，已有结果保留。</Typography.Paragraph>
    {error && <Alert type="error" showIcon message={error} action={!draft ? <Button onClick={() => setReload(v => v + 1)}>重试</Button> : undefined} />}
    {loading && <Spin />}
    {draft && <div style={{ '--reading-border': token.colorBorderSecondary, '--reading-muted': token.colorTextSecondary } as React.CSSProperties}>
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
        <Typography.Paragraph type="secondary">仅用于论文阅读，与聊天模型及全局默认独立。结构解析支持 MinerU；切换后保留并优先复用已有结果，不会自动重新生成。</Typography.Paragraph>
      </div>
      <Radio.Group aria-label="准备预设" className="reading-preset-options" disabled={saving} value={draft.preset} onChange={event => {
        const preset = event.target.value as ReadingPreset;
        setDraft({ ...draft, preset, modes: preset === 'custom' ? draft.modes : presetModes(preset) });
      }}>
        {presets.map(([value, title, description]) => <Radio key={value} value={value}><strong>{title}</strong><span>{description}</span></Radio>)}
        <Radio value="custom">自定义</Radio>
      </Radio.Group>
      <div className="reading-capabilities">
        {Object.entries(names).map(([key, name]) => {
          const id = key as ReadingCapabilityId; const capability = draft.capabilities.find(c => c.id === id);
          return <div className="reading-capability" key={id}><div><strong>{name}</strong><p>{id === 'chat_prepare' ? '为文档对话准备可检索文本，与结构解析独立。' : id === 'pdf_parse' ? '识别正文、公式、图片与表格，供后续研读使用。' : '每个条目可能调用模型，已有结果优先复用。'}</p>
            {capability?.unavailable_reason && <Typography.Text type="warning">{capability.unavailable_reason}</Typography.Text>}</div>
            <Select aria-label={name} value={draft.modes[id]} disabled={saving} options={[{ value: 'on_demand', label: '按需' }, { value: 'automatic', label: '自动' }, { value: 'disabled', label: '禁用生成' }]} onChange={mode => setDraft({ ...draft, preset: 'custom', modes: { ...draft.modes, [id]: mode } })} />
          </div>;
        })}
        <div className="reading-capability"><div><strong>论文速览</strong><p>论文速览暂不可用。</p></div><Select aria-label="论文速览" disabled value="unavailable" options={[{ value: 'unavailable', label: '暂不可用' }]} /></div>
      </div>
      {dependencyError && <Alert type="warning" showIcon message={dependencyError} />}
      <Collapse ghost items={[{ key: 'limits', label: '费用与数量限制', children: <div className="reading-limits">
        <label>每天自动准备论文<InputNumber aria-label="每天自动准备论文" disabled={saving} min={1} max={100} value={draft.limits.daily_documents} onChange={value => value != null && setDraft({ ...draft, limits: { ...draft.limits, daily_documents: value } })} />篇</label>
        <label>每篇研读条目总数<InputNumber aria-label="每篇研读条目总数" disabled={saving} min={1} max={100} value={draft.limits.study_items_per_document} onChange={value => value != null && setDraft({ ...draft, limits: { ...draft.limits, study_items_per_document: value } })} />项</label>
        <Typography.Text type="secondary">每日额度按 UTC 日期计算。公式、图片、表格合计计数；自动任务串行执行（并发 1）。次数限制不是费用金额上限。</Typography.Text>
      </div> }]} />
      <Typography.Paragraph type="secondary">自动准备会使用已配置的解析服务和模型，可能产生费用。默认每天 5 篇、每篇合计 10 项。禁用生成后仍可查看已有结果；正在执行的请求可能继续计费。</Typography.Paragraph>
    </div>}
  </Modal>;
}
