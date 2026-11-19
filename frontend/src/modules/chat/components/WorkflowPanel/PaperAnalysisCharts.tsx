import { useContext, useEffect, useMemo, useState } from 'react';
import { Image } from 'antd';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';
import { resolveMarkdownImageUrlAsync } from '@/modules/knowledge/utils/imageUrl';
import { SlotDownloadContext } from './SlotComponents';
import { groupPaperCharts, sourceOf, formats, type Format, type ChartFile, type ChartGroup } from './paperCharts';
import './PaperAnalysisCharts.scss';
export { groupPaperCharts } from './paperCharts';
type ChartDescription = { id: string; title: string; caption: string };

export function paperChartDescriptions(value: unknown): ChartDescription[] {
  if (!value || typeof value !== 'object') return [];
  const envelope = value as Record<string, any>;
  const pool = Array.isArray(envelope.materials) ? envelope : envelope.data;
  if (!pool || !['paper-material-pool.v1', 'paper-material-pool.v2'].includes(pool.schema) || !Array.isArray(pool.materials)) return [];
  const reference = pool.current?.charts;
  const material = pool.materials.find((item: any) => item.kind === 'charts'
    && item.id === reference?.id && item.version === reference?.version);
  if (!Array.isArray(material?.content)) return [];
  return material.content.filter((item: any) => typeof item.id === 'string' && typeof item.title === 'string')
    .map((item: any) => ({ id: item.id, title: item.title, caption: typeof item.caption === 'string' ? item.caption : '' }));
}

function ChartCard({ group, description }: { group: ChartGroup; description?: ChartDescription }) {
  const downloadEnabled = useContext(SlotDownloadContext);
  const [urls, setUrls] = useState<Partial<Record<Format, string>>>({});
  const [failedImages, setFailedImages] = useState<Format[]>([]);
  const [loading, setLoading] = useState(true);
  const [retry, setRetry] = useState(0);
  const files = formats.map(format => group.files[format]).filter((file): file is ChartFile => Boolean(file));
  const identity = JSON.stringify(files.map(file => [file.format, file.source]));
  useEffect(() => {
    let cancelled = false;
    setUrls({}); setFailedImages([]); setLoading(true);
    void Promise.all(files.map(async file => {
      try { return [file.format, await resolveMarkdownImageUrlAsync(file.source)] as const; }
      catch { return [file.format, ''] as const; }
    })).then(resolved => {
      if (!cancelled) { setUrls(Object.fromEntries(resolved)); setLoading(false); }
    });
    return () => { cancelled = true; };
  }, [identity, retry]);
  const preview = (['png', 'svg'] as const).find(format => urls[format] && !failedImages.includes(format));
  const primary = group.files.svg ?? group.files.png ?? group.files.pdf!;
  const title = description?.title || group.id;
  const caption = description?.caption || primary.revision.caption || primary.revision.artifact_value?.caption || '';
  const version = primary.revision.version_number ?? primary.revision.revision;
  return <figure className='paper-analysis-chart'>
    <figcaption className='paper-analysis-chart__heading'>
      <h3>{title}</h3><span className='paper-analysis-chart__version'>v{version}</span>
    </figcaption>
    <div className='paper-analysis-chart__preview'>
      {loading ? <p role='status'>正在加载图表…</p> : preview ? <Image
        src={urls[preview]} alt={title} loading='lazy' width='100%'
        onError={() => setFailedImages(current => [...current, preview])}
      /> : <div role='status'><p>图表预览暂时无法加载，仍可下载原文件。</p>
        <button type='button' onClick={() => setRetry(current => current + 1)}>重新加载</button></div>}
    </div>
    {caption && <p className='paper-analysis-chart__caption'>{caption}</p>}
    {downloadEnabled && <nav className='paper-analysis-chart__downloads' aria-label={`${title} 下载格式`}>
      {files.map(file => urls[file.format] && <a key={file.format}
        href={`${urls[file.format]}${urls[file.format]!.includes('?') ? '&' : '?'}download=1`}
        download={file.name} aria-label={`下载 ${title} ${file.format.toUpperCase()}`}>
        {file.format.toUpperCase()}
      </a>)}
    </nav>}
  </figure>;
}

/** F4-only view over existing file artifacts; old sessions need no migration or model rerun. */
export function PaperAnalysisCharts({ revisions, metadataSlots }: {
  revisions: SlotRevision[]; metadataSlots: SlotRevision[];
}) {
  const groups = useMemo(() => groupPaperCharts(revisions), [revisions]);
  const metadata = metadataSlots.filter(slot => slot.slot === 'data_analysis_pool' && slot.selected
    && (!slot.step_id || slot.step_id === 'data_analysis'))
    .sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at) || b.revision - a.revision)[0];
  const [descriptions, setDescriptions] = useState<ChartDescription[]>([]);
  const [metadataFailed, setMetadataFailed] = useState(false);
  const [retry, setRetry] = useState(0);
  const source = sourceOf(metadata?.artifact_value);
  const inline = metadata?.artifact_value?.data;
  const identity = JSON.stringify([source, inline, metadata?.revision, metadata?.draft_version]);
  useEffect(() => {
    const abort = new AbortController();
    setDescriptions([]); setMetadataFailed(false);
    if (inline) { setDescriptions(paperChartDescriptions(inline)); return () => abort.abort(); }
    if (!source) return () => abort.abort();
    void resolveMarkdownImageUrlAsync(source).then(async url => {
      if (abort.signal.aborted) return;
      const response = await fetch(url, { signal: abort.signal, credentials: 'same-origin' });
      if (!response.ok) throw new Error('Chart metadata unavailable');
      const data = paperChartDescriptions(await response.json());
      if (!abort.signal.aborted) { setDescriptions(data); setMetadataFailed(data.length === 0); }
    }).catch(() => { if (!abort.signal.aborted) setMetadataFailed(true); });
    return () => abort.abort();
  }, [identity, retry]);
  return <div className='paper-analysis-charts'>
    {metadataFailed && <p className='paper-analysis-charts__notice' role='status'>
      图表标题与说明暂时无法加载，先显示图表编号。
      <button type='button' onClick={() => setRetry(current => current + 1)}>重试说明</button>
    </p>}
    {groups.map(group => <ChartCard key={group.id} group={group}
      description={descriptions.find(item => item.id === group.id)} />)}
    {groups.length === 0 && <p role='status'>暂无可展示的分析图表。</p>}
  </div>;
}
