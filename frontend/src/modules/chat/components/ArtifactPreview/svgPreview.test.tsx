import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import ArtifactPreview from './index';
import { downloadStream } from '../../utils/download';

vi.mock('../../utils/download', () => ({ downloadStream: vi.fn() }));
vi.mock('@/modules/knowledge/components/FileViewer', () => ({ default: () => null }));

it('shows SVG as a scaled image, supports source view and downloads the original SVG', async () => {
  const svg = '<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1440 1000"><title>架构图</title><script>alert(1)</script><rect width="20" height="20" /></svg>';
  const target = { key: 'svg', filename: 'diagram.svg', content: svg };
  const view = render(<ArtifactPreview target={target} onClose={vi.fn()} />);
  await waitFor(() => expect(view.container.querySelector('iframe')).toBeInTheDocument());
  const frame = view.container.querySelector('iframe')!;
  const doc = new DOMParser().parseFromString(frame.getAttribute('srcdoc')!, 'text/html');
  expect(doc.querySelector('img')?.getAttribute('src')).toBe(`data:image/svg+xml,${encodeURIComponent(svg)}`);
  expect(doc.querySelector('script')).toBeNull();
  expect(doc.querySelector('style')?.textContent).toContain('width:100%;height:auto');
  expect(frame.getAttribute('sandbox')).not.toContain('allow-scripts');
  fireEvent.click(screen.getByRole('button', { name: '源码' }));
  expect(view.container.querySelector('pre')).toHaveTextContent('架构图');
  fireEvent.click(screen.getByRole('button', { name: '预览' }));
  expect(view.container.querySelector('iframe')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '下载文件' }));
  const [blob, filename] = vi.mocked(downloadStream).mock.calls[0];
  expect(filename).toBe('diagram.svg');
  expect(blob.type).toBe('image/svg+xml;charset=utf-8');
  const reader = new FileReader();
  const contents = new Promise(resolve => { reader.onload = () => resolve(reader.result); });
  reader.readAsText(blob);
  expect(await contents).toBe(svg);
});
