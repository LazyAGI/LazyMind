import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';
import { paperChartMediaUrls } from './paperCharts';
import { PaperAnalysisCharts, paperChartDescriptions } from './PaperAnalysisCharts';

vi.mock('./SlotComponents', async () => ({ SlotDownloadContext: (await import('react')).createContext(true) }));
vi.mock('@/modules/knowledge/utils/imageUrl', () => ({
  resolveMarkdownImageUrlAsync: async (source: string) => source,
}));
afterEach(cleanup);

function revision(slot: string, artifact_value: unknown): SlotRevision {
  return { slot_id: slot, slot, step_id: 'data_analysis', revision: 1, selected: true,
    created_at: '2026-01-01T00:00:00Z', artifact_value };
}
const files = ['svg', 'png', 'pdf'].map(format => revision('analysis_chart', {
  filename: `分析图表-means.${format}`, path: `/static-files/means.${format}`,
}));
const metadata = [revision('data_analysis_pool', { data: {
  schema: 'paper-material-pool.v1', current: { charts: { id: 'charts', version: 1 } },
  materials: [{ id: 'charts', version: 1, kind: 'charts', content: [
    { id: 'means', title: 'Mean scores', caption: 'Synthetic group means.' },
  ] }],
} })];

it.each(['分析图表-', ''])('groups %s exports into one figure with title and downloads', async prefix => {
  const chartFiles = files.map(file => ({ ...file, artifact_value: { ...file.artifact_value, filename: `${prefix}means.${file.artifact_value.filename.split('.').pop()}` } }));
  render(<PaperAnalysisCharts revisions={chartFiles} metadataSlots={metadata} />);
  const image = await screen.findByRole('img', { name: 'Mean scores' });
  expect(image).toHaveAttribute('src', '/static-files/means.png');
  expect(screen.getByRole('heading', { name: 'Mean scores' })).toBeInTheDocument();
  expect(screen.getByText('Synthetic group means.')).toBeInTheDocument();
  expect(document.querySelectorAll('figure')).toHaveLength(1);
  for (const format of ['SVG', 'PNG', 'PDF']) {
    expect(screen.getByRole('link', { name: `下载 Mean scores ${format}` }))
      .toHaveAttribute('href', `/static-files/means.${format.toLowerCase()}?download=1`);
  }
});

it('falls back to SVG and keeps downloads accessible when previews fail', async () => {
  render(<PaperAnalysisCharts revisions={files} metadataSlots={metadata} />);
  const image = await screen.findByRole('img', { name: 'Mean scores' });
  fireEvent.error(image);
  await waitFor(() => expect(image).toHaveAttribute('src', '/static-files/means.svg'));
  fireEvent.error(image);
  expect(await screen.findByText('图表预览暂时无法加载，仍可下载原文件。')).toBeInTheDocument();
  expect(screen.getAllByRole('link')).toHaveLength(3);
  fireEvent.click(screen.getByRole('button', { name: '重新加载' }));
  expect(await screen.findByRole('img', { name: 'Mean scores' }))
    .toHaveAttribute('src', '/static-files/means.png');
});


it.each(['paper-material-pool.v1', 'paper-material-pool.v2'])('reads figure metadata from %s', schema => {
  const value = { ...metadata[0].artifact_value.data, schema };
  expect(paperChartDescriptions({ data: value })).toEqual([
    { id: 'means', title: 'Mean scores', caption: 'Synthetic group means.' },
  ]);
  expect(paperChartDescriptions({ ...value, schema: 'unknown' })).toEqual([]);
});


it('maps paper placeholders only to selected chart exports', () => {
  expect(paperChartMediaUrls([
    ...files.map(file => ({ ...file, artifact_value: { ...file.artifact_value,
      filename: file.artifact_value.filename.replace('分析图表-', '') } })),
    { ...revision('analysis_chart', { filename: 'obsolete.svg', path: '/static-files/old.svg' }), selected: false },
    revision('other', { filename: 'other.svg', path: '/static-files/other.svg' }),
  ])).toEqual({ 'media-placeholder://means': '/static-files/means.svg' });
});
