import type { SlotRevision } from '@/modules/chat/store/workflowPanel';

export type Format = 'svg' | 'png' | 'pdf';
export type ChartFile = { format: Format; name: string; source: string; revision: SlotRevision };
export type ChartGroup = { id: string; files: Partial<Record<Format, ChartFile>> };
export const formats: Format[] = ['svg', 'png', 'pdf'];

export function sourceOf(value: unknown): string {
  if (!value || typeof value !== 'object') return '';
  const raw = value as Record<string, unknown>;
  const source = String(raw.url ?? raw.path ?? '');
  return /^(?:https?:\/\/|\/(?:api\/core\/)?static-files\/|\/data\/subagent\/|\/var\/lib\/lazymind\/uploads\/)/i.test(source) ? source : '';
}

/** Group selected export files without modifying their durable slot indices. */
export function groupPaperCharts(revisions: SlotRevision[]): ChartGroup[] {
  const groups = new Map<string, ChartGroup>();
  for (const revision of revisions) {
    if (!revision.selected) continue;
    const raw = revision.artifact_value;
    const name = String(raw?.filename ?? raw?.name ?? '');
    const match = /^(?:分析图表-)?(.+)\.(svg|png|pdf)$/i.exec(name);
    const source = sourceOf(raw);
    if (!match || !source) continue;
    const id = match[1], format = match[2].toLowerCase() as Format;
    const group = groups.get(id) ?? { id, files: {} };
    const previous = group.files[format];
    if (!previous || Date.parse(revision.created_at) >= Date.parse(previous.revision.created_at)) {
      group.files[format] = { format, name, source, revision };
    }
    groups.set(id, group);
  }
  return [...groups.values()];
}

export function paperChartMediaUrls(slots: SlotRevision[]): Record<string, string> {
  const charts = slots.filter(slot => (slot.slot ?? slot.slot_id) === 'analysis_chart');
  return Object.fromEntries(groupPaperCharts(charts).flatMap(group => {
    const file = group.files.svg ?? group.files.png;
    return file ? [[`media-placeholder://${group.id}`, file.source]] : [];
  }));
}
