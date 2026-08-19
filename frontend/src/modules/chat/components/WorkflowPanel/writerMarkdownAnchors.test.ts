import { describe, expect, it } from 'vitest';
import { isWriterSystemAnchorBlock } from './writerIR';
import {
  applyWriterMarkdownInternalReference,
  collectWriterMarkdownOutline,
  collectWriterMarkdownReferenceTargets,
  protectWriterMarkdownHeadingAnchors,
  removeWriterMarkdownInternalReference,
  restoreWriterMarkdownInternalReferenceLabels,
  writerMarkdownForEditor,
  writerMarkdownForEditing,
  writerMarkdownForSave,
  writerMarkdownInternalReference,
} from './writerMarkdownAnchors';

describe('Writer Markdown system anchors', () => {
  it('keeps system anchors round-trippable through MDXEditor', () => {
    const source = '# 标题\n\n<a id="block-sec-1"></a>\n## 1 章节';
    const editorValue = writerMarkdownForEditor(source);

    expect(editorValue).toContain('<a id="block-sec-1" />');
    expect(writerMarkdownForSave(editorValue)).toBe(source);
  });

  it('does not rewrite unrelated HTML anchors', () => {
    const source = '<a id="custom"></a>正文';

    expect(writerMarkdownForEditor(source)).toBe(source);
    expect(writerMarkdownForSave(source)).toBe(source);
  });

  it('keeps heading anchors out of the editable document without leaving blank blocks', () => {
    const source = [
      '# 标题',
      '',
      '',
      '<a id="block-sec-1"></a>',
      '## 1 章节',
      '',
      '<a id="block-image-1"></a>',
      '![插图](https://example.com/image.png)',
    ].join('\n');

    expect(writerMarkdownForEditing(source)).toBe([
      '# 标题',
      '',
      '## 1 章节',
      '',
      '<a id="block-image-1" />',
      '![插图](https://example.com/image.png)',
    ].join('\n'));
  });

  it('restores stable heading anchors on save and removes anchors for deleted headings', () => {
    const source = [
      '# 标题',
      '',
      '<a id="block-sec-1"></a>',
      '## 1 第一章',
      '',
      '<a id="block-sec-2"></a>',
      '## 2 第二章',
    ].join('\n');
    const edited = ['# 标题', '', '## 2 第二章'].join('\n');
    const restored = protectWriterMarkdownHeadingAnchors(source, edited);

    expect(restored).toContain('<a id="block-sec-2" />\n## 2 第二章');
    expect(restored).not.toContain('block-sec-1');
    expect(restored).not.toContain('\n\n\n');
  });

  it('assigns an anchor when a document starts with a section heading', () => {
    const restored = protectWriterMarkdownHeadingAnchors('', '## 新章节');

    expect(restored).toMatch(/^<a id="block-user-[^"]+" \/>\n## 新章节$/);
  });

  it('identifies only IR paragraphs that contain a system anchor', () => {
    expect(isWriterSystemAnchorBlock({
      node_id: 'anchor-1',
      type: 'paragraph',
      content: '<a id="block-sec-1" />',
    })).toBe(true);
    expect(isWriterSystemAnchorBlock({
      node_id: 'paragraph-1',
      type: 'paragraph',
      content: '<a id="custom"></a>',
    })).toBe(false);
  });

  it('collects paired and editor-form anchors with their heading labels', () => {
    const source = [
      '<a id="block-sec-1"></a>',
      '## 1 系统设计',
      '',
      '<a id="block-sec-2" />',
      '### 1.1 接口设计',
    ].join('\n');

    expect(collectWriterMarkdownReferenceTargets(source)).toEqual([
      { anchorId: 'block-sec-1', label: '1 系统设计', type: 'heading' },
      { anchorId: 'block-sec-2', label: '1.1 接口设计', type: 'heading' },
    ]);
  });

  it('collects anchored images as reference targets without adding them to the outline', () => {
    const source = [
      '<a id="block-sec-1"></a>',
      '## 1 系统设计',
      '',
      '<a id="block-image-1"></a>',
      '![图1 雨后山间溪流图](https://example.com/rain.png)',
      '',
      '<a id="block-image-2" />',
      '![](https://example.com/forest.png)',
      '',
      '![未锚定图片](https://example.com/unanchored.png)',
      '',
      '```markdown',
      '<a id="block-image-fake" />',
      '![代码块内图片](https://example.com/fake.png)',
      '```',
      '',
      '    ```markdown',
      '    <a id="block-heading-indented-fake" />',
      '    ## 缩进代码块内标题',
      '    <a id="block-image-indented-fake" />',
      '    ![缩进代码块内图片](https://example.com/indented-fake.png)',
      '    ```',
    ].join('\n');

    expect(collectWriterMarkdownReferenceTargets(source)).toEqual([
      { anchorId: 'block-sec-1', label: '1 系统设计', type: 'heading' },
      { anchorId: 'block-image-1', label: '图1 雨后山间溪流图', type: 'image' },
      { anchorId: 'block-image-2', label: 'block-image-2', type: 'image' },
    ]);
    expect(collectWriterMarkdownOutline(source).items).toEqual([
      { anchorId: 'block-sec-1', label: '1 系统设计', level: 2 },
    ]);
  });

  it('collects the Markdown title and anchored heading levels for the table of contents', () => {
    const source = [
      '# 产品架构说明',
      '',
      '<a id="block-sec-1"></a>',
      '## 1 系统设计',
      '',
      '<a id="block-sec-2" />',
      '### 1.1 接口设计',
      '',
      '```markdown',
      '<a id="block-fake" />',
      '## 代码块内标题',
      '```',
    ].join('\n');

    expect(collectWriterMarkdownOutline(source)).toEqual({
      title: '产品架构说明',
      items: [
        { anchorId: 'block-sec-1', label: '1 系统设计', level: 2 },
        { anchorId: 'block-sec-2', label: '1.1 接口设计', level: 3 },
      ],
    });
  });

  it('constructs an internal Markdown link from selected text and an anchor', () => {
    expect(writerMarkdownInternalReference('第 1 节', 'block-sec-1'))
      .toBe('[第 1 节](#block-sec-1)');
  });

  it('adds a reference around the original selected wording without moving it', () => {
    const paragraph = '潮水退去后，他仍听见深渊的低语。';
    const source = `# 标题\n\n${paragraph}`;

    expect(applyWriterMarkdownInternalReference(source, paragraph, 6, '他仍听见', 'block-sec-1'))
      .toBe('# 标题\n\n潮水退去后，[他仍听见](#block-sec-1)深渊的低语。');
  });

  it('locates a later selection when the paragraph already contains a reference', () => {
    const source = '详见[前文](#block-sec-1)，他仍听见深渊的低语。';
    const paragraph = '详见前文，他仍听见深渊的低语。';

    expect(applyWriterMarkdownInternalReference(source, paragraph, 5, '他仍听见', 'block-sec-2'))
      .toBe('详见[前文](#block-sec-1)，[他仍听见](#block-sec-2)深渊的低语。');
  });

  it('unwraps the internal link containing the selection and keeps its target anchor', () => {
    const source = [
      '详见[前文约定](#block-sec-1)。',
      '',
      '<a id="block-sec-1"></a>',
      '## 1 前文',
    ].join('\n');
    const revised = removeWriterMarkdownInternalReference(
      source,
      '详见前文约定。',
      3,
      '文约',
    );

    expect(revised).toContain('详见前文约定。');
    expect(revised).not.toContain('](#block-sec-1)');
    expect(revised).toContain('<a id="block-sec-1"></a>');
  });

  it('keeps escaped link labels intact when removing the reference', () => {
    const source = '详见[前文\\]约定](#block-sec-1)。';

    expect(removeWriterMarkdownInternalReference(source, '详见前文]约定。', 4, ']约'))
      .toBe('详见前文\\]约定。');
  });

  it('does not remove external links or ambiguous repeated references', () => {
    const external = '详见[前文](https://example.com)。';
    expect(removeWriterMarkdownInternalReference(external, '详见前文。', 2, '前文'))
      .toBe(external);

    const repeated = [
      '详见[前文](#block-sec-1)。',
      '',
      '详见[前文](#block-sec-1)。',
    ].join('\n');
    expect(removeWriterMarkdownInternalReference(repeated, '详见前文。', 2, '前文'))
      .toBe(repeated);

    const repeatedWithPlainText = [
      '详见[前文](#block-sec-1)。',
      '',
      '详见前文。',
    ].join('\n');
    expect(removeWriterMarkdownInternalReference(
      repeatedWithPlainText,
      '详见前文。',
      2,
      '前文',
    )).toBe(repeatedWithPlainText);

    const repeatedWithExternalLink = [
      '详见[前文](#block-sec-1)。',
      '',
      '详见[前文](https://example.com)。',
    ].join('\n');
    expect(removeWriterMarkdownInternalReference(
      repeatedWithExternalLink,
      '详见前文。',
      2,
      '前文',
    )).toBe(repeatedWithExternalLink);
  });

  it('restores the user wording after server numbering materialization', () => {
    const source = '详见[前文的约定](#block-sec-1)。';
    const materialized = '详见[第1章](#block-sec-1)。';

    expect(restoreWriterMarkdownInternalReferenceLabels(materialized, source)).toBe(source);
  });
});
