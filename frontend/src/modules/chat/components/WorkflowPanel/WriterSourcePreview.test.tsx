import { render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { WriterSourcePreview } from './WriterSourcePreview';
import { writerSafeUrl, writerSourceMarkdown } from './writerSourceSyntax';
import { createHash, webcrypto } from 'node:crypto';
vi.mock('../MarkdownViewer/MermaidBlock', () => ({default: ({code}:{code:string})=><div data-testid='diagram'>{code}</div>}));
vi.mock('@/modules/knowledge/utils/imageUrl', () => ({resolveMarkdownImageUrlAsync:async (value:string)=>value}));
vi.mock('react-i18next', () => ({useTranslation:()=>({t:(key:string)=>key})}));

describe('Writer source preview',()=>{
 it('renders diagrams, formulas and native tables without changing source',()=>{
  const source='```mermaid\nA --> B\n```\n\n$$x^2$$\n\n| A | B |\n|---|---|\n|1|2|';
  const {container}=render(<WriterSourcePreview source={source} />);
  expect(screen.getByTestId('diagram')).toHaveTextContent('A --> B');
  expect(container.querySelector('.katex')).not.toBeNull();
  expect(screen.getByRole('table')).toHaveTextContent('A');
 });
 it('supports folded callouts and hides comments while preserving inline code',()=>{
  const {container}=render(<WriterSourcePreview source={'> [!note]- Important\n> body\n\n%% secret %%\n\n`[[literal]]`'} />);
  expect(screen.getByText('Important').tagName).toBe('SUMMARY');
  expect(container.querySelector('details')).not.toHaveAttribute('open');
  expect(container).not.toHaveTextContent('secret');
  expect(container).toHaveTextContent('[[literal]]');
 });
 it('keeps external wiki targets as labels until advanced adaptation is implemented',()=>{
  render(<WriterSourcePreview source='[[notes/design.md|Design]]' />);
  expect(screen.queryByRole('link')).toBeNull();
  expect(screen.getByText('Design')).toBeInTheDocument();
 });
 it('uses the existing image resolver and preserves dimensions',async()=>{
  render(<WriterSourcePreview source='![[chart.png|300x200]]' resolveImage={async()=>'/static-files/chart.png'} />);
  await waitFor(()=>expect(screen.getByRole('presentation')).toHaveAttribute('width','300'));
 });
 it('does not render scripts or unsafe media links',async()=>{
  const {container}=render(<WriterSourcePreview source={'<script>alert(1)</script>\n\n<img src="javascript:alert(1)" onerror="alert(1)">\n\n[bad](javascript:alert(1))'} />);
  expect(container.querySelector('script')).toBeNull();
  expect(container.querySelector('[onerror]')).toBeNull();
  expect(container.querySelector('[href^="javascript:"]')).toBeNull();
  expect(writerSafeUrl('javascript:alert(1)')).toBeUndefined();
  await screen.findByRole('button',{name:'chat.writerSource.retry'});
 });
 it('keeps source fences intact',()=>{
  expect(writerSourceMarkdown('```\n[[literal]]\n%%literal%%\n```')).toContain('[[literal]]\n%%literal%%');
 });
});

it('adapts existing media URLs with native controls and no executable HTML', () => {
 const {container}=render(<WriterSourcePreview source={'<video src="https://example.org/demo.mp4" autoplay></video>\n\n<audio src="https://example.org/demo.mp3"></audio>'} />);
 expect(container.querySelector('video')).toHaveAttribute('controls');
 expect(container.querySelector('video')).not.toHaveAttribute('autoplay');
 expect(container.querySelector('audio')).toHaveAttribute('controls');
});

it('consumes source display context and drops it immediately when the document changes',async()=>{
 vi.stubGlobal('crypto',webcrypto);
 const source='![image](image.png)';
 const renderContext={source_hash:createHash('sha256').update(source).digest('hex'),code_fences:[],images:[{start:0,end:source.length,width:300,height:200}]};
 const {rerender}=render(<WriterSourcePreview source={source} renderContext={renderContext} />);
 await waitFor(()=>expect(screen.getByRole('img')).toHaveAttribute('width','300'));
 rerender(<WriterSourcePreview source={'Changed\n\n'+source} renderContext={renderContext} />);
 await waitFor(()=>expect(screen.getByRole('img')).not.toHaveAttribute('width'));
 vi.unstubAllGlobals();
});
