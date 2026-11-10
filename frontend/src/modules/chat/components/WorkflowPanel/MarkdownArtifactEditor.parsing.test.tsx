import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MarkdownArtifactEditor } from './MarkdownArtifactEditor';

afterEach(() => vi.unstubAllGlobals());

describe('MarkdownArtifactEditor real Markdown parsing', () => {
  it.each(['img', 'spring'])('renders a bare <%s> in a material summary as text', async (tag) => {
    const onSave = vi.fn(async () => 2);
    const { container } = render(
      <MarkdownArtifactEditor
        markdown={`# 素材收集结果\n\n页面 HTML 作为前景 <${tag}> 嵌入（叠蒙版使用）。\n\n## 后续步骤\n\n继续生成页面。`}
        sourceRevision={1}
        onSave={onSave}
      />,
    );

    await waitFor(() => expect(container.querySelector('h1')).toHaveTextContent('素材收集结果'));
    expect(container.querySelector('.mdxeditor-root-contenteditable'))
      .toHaveTextContent(`页面 HTML 作为前景 <${tag}> 嵌入（叠蒙版使用）。`);
    expect(container.querySelector('h2')).toHaveTextContent('后续步骤');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    const options = container.querySelector('details.writer-document-options')!;
    options.setAttribute('open', '');
    fireEvent(options, new Event('toggle'));
    fireEvent.click(screen.getByRole('button', { name: '源码' }));
    expect(screen.getByRole('textbox', { name: '源码' }).getAttribute('readonly')).not.toBeNull();
    expect(screen.getByRole('textbox', { name: '源码' })).toHaveValue(
      `# 素材收集结果\n\n页面 HTML 作为前景 <${tag}> 嵌入（叠蒙版使用）。\n\n## 后续步骤\n\n继续生成页面。`,
    );
    expect(onSave).not.toHaveBeenCalled();
  });

  it('preserves real images, links, anchors and code alongside literal tags', async () => {
    // jsdom does not load images; complete only the image preload, keeping the
    // real MDX parser, image plugin and rendered image attributes under test.
    vi.stubGlobal('Image', class {
      onload?: () => void;
      set src(_value: string) { queueMicrotask(() => this.onload?.()); }
    });
    const markdown = [
      '# 素材',
      '',
      '标签示例 <spring> 与 <img>。',
      '',
      '行内代码：`<spring>`、`<img>`。',
      '',
      '```html',
      '<spring>',
      '<img>',
      '```',
      '',
      '<a id="paper-ref-1" />',
      '',
      '[参考来源](#paper-ref-1) 与 [外部来源](https://example.com)。',
      '',
      '![素材图](</static-files/material.png>)',
      '',
      '<img src="/static-files/preview.png" alt="预览图" />',
      '',
      '保留 <u>下划线</u> 和 x<sup>2</sup>。',
    ].join('\n');
    const { container } = render(
      <MarkdownArtifactEditor
        markdown={markdown}
        sourceRevision={1}
        onSave={vi.fn(async () => 2)}
        resolveImageUrl={async (url) => url}
      />,
    );

    await waitFor(() => expect(container.querySelector('h1')).toHaveTextContent('素材'));
    const content = container.querySelector('.mdxeditor-root-contenteditable')!;
    expect(content).toHaveTextContent('标签示例 <spring> 与 <img>。');
    expect(content.querySelector('code')).toHaveTextContent('<spring>');
    expect(content.querySelector('[id="paper-ref-1"]')).toBeInTheDocument();
    expect(content.querySelector('a[href="#paper-ref-1"]')).toHaveTextContent('参考来源');
    expect(content.querySelector('a[href="https://example.com"]')).toHaveTextContent('外部来源');
    await waitFor(() => expect(content.querySelector('img[alt="素材图"]'))
      .toHaveAttribute('src', '/static-files/material.png'));
    expect(content.querySelector('img[alt="预览图"]')).toHaveAttribute('src', '/static-files/preview.png');
    expect(content.querySelector('[class*="underline"]')).toHaveTextContent('下划线');
    expect(content.querySelector('sup')).toHaveTextContent('2');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });
});
