import { describe, expect, it } from 'vitest';
import { isWriterSystemAnchorBlock } from './writerIR';
import {
  applyWriterMarkdownInternalReference,
  collectWriterMarkdownDomAnchors,
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
  it.each(['新增段落', '新增段落\n\n第二段'])('moves the old sidecar when inserting %s before a heading', (inserted) => {
    const anchor = '<a id="block-sec-1" numbering="restart"></a>';
    const source = `${anchor}\n## 原标题\n\n[引用](#block-sec-1)`;
    const edited = `${anchor}\n${inserted}\n\n## 原标题\n\n[引用](#block-sec-1)`;
    const saved = writerMarkdownForSave(protectWriterMarkdownHeadingAnchors(source, edited, true, true));
    expect(saved).toBe(`${inserted}\n\n${anchor}\n## 原标题\n\n[引用](#block-sec-1)`);
    expect(writerMarkdownForSave(protectWriterMarkdownHeadingAnchors(saved, saved, true, true))).toBe(saved);
  });

  it('moves an image sidecar without touching paragraph, custom, or fenced anchors', () => {
    const anchor = '<a id="block-image-1"></a>';
    const untouched = '<a id="custom"></a>\n<a id="block-paragraph-1"></a>\n正文\n\n```html\n'
      + anchor + '\n```\n\n';
    const image = '![图](image.png)';
    const source = `${untouched}${anchor}\n${image}`;
    const edited = `${untouched}${anchor}\n新增段落\n\n${image}`;
    expect(writerMarkdownForSave(protectWriterMarkdownHeadingAnchors(source, edited, true, true)))
      .toBe(`${untouched}新增段落\n\n${anchor}\n${image}`);
  });

  it('gives a pasted duplicate heading its own id while keeping the original reference target', () => {
    const source = '<a id="block-sec-1"></a>\n## 标题';
    const saved = writerMarkdownForSave(protectWriterMarkdownHeadingAnchors(source, `${source}\n\n${source}`));
    const ids = [...saved.matchAll(/<a id="([^"]+)"/g)].map(match => match[1]);
    expect(ids).toHaveLength(2);
    expect(ids[0]).toBe('block-sec-1');
    expect(ids[1]).not.toBe(ids[0]);
  });

  it('preserves source whitespace and mixed line endings when adding navigation anchors', () => {
    const source = '# Title\r\n\r\n\r\n## Section\n\n\n![Image](image.png)\r\n\r\n';
    const saved = writerMarkdownForSave(protectWriterMarkdownHeadingAnchors(source, source, true, true));
    expect(saved).toContain('<a id="block-user-');
    expect(saved.replace(/^<a id="block-user-[^"]+"><\/a>\r?\n/gm, '')).toBe(source);
    expect(writerMarkdownForSave(protectWriterMarkdownHeadingAnchors(saved, saved, true, true))).toBe(saved);
  });

  it('keeps CRLF frontmatter and the numbering sidecar in source-preserving saves', () => {
    const source = '---\r\ntitle: Test\r\n---\r\n<!-- heading-numbering: {"ordered_style":"chinese"} -->\r\n# Title\r\n\r\n<a id="block-sec-1"></a>\r\n## Section\r\n';
    const saved = writerMarkdownForSave(protectWriterMarkdownHeadingAnchors(source, source, true, true));
    expect(saved).toBe(source);
  });

  it.each(['#', '##', '###', '####', '#####', '######'])('preserves %s numbering when its text is cleared and retyped', (prefix) => {
    const source = `<a id="block-sec-1" numbering="restart"></a>\n${prefix} 1 章节\n\n<a id="block-sec-2"></a>\n## 后续章节`;
    const cleared = `${prefix}\n\n## 后续章节`;
    const saved = writerMarkdownForSave(protectWriterMarkdownHeadingAnchors(source, cleared));

    expect(saved).toBe(`<a id="block-sec-1" numbering="restart"></a>\n${prefix}\n\n<a id="block-sec-2"></a>\n## 后续章节`);
    expect(collectWriterMarkdownDomAnchors(saved)).toEqual([
      { anchorId: 'block-sec-1', type: 'heading', targetIndex: 0 },
      { anchorId: 'block-sec-2', type: 'heading', targetIndex: 1 },
    ]);
    expect(collectWriterMarkdownOutline(saved).items[0]).toMatchObject({
      anchorId: 'block-sec-1', label: `H${prefix.length}`, level: prefix.length,
    });
    expect(collectWriterMarkdownReferenceTargets(saved)[0]).toMatchObject({
      anchorId: 'block-sec-1', label: `H${prefix.length}`,
    });
    expect(writerMarkdownForSave(protectWriterMarkdownHeadingAnchors(
      saved, `${prefix} 新标题\n\n## 后续章节`,
    ))).toContain(`<a id="block-sec-1" numbering="restart"></a>\n${prefix} 新标题`);
  });

  it('counts empty headings without assigning later anchors to the wrong DOM heading', () => {
    expect(collectWriterMarkdownDomAnchors('#\n\n##\n\n<a id="block-sec-2"></a>\n## 章节')).toEqual([
      { anchorId: 'block-sec-2', type: 'heading', targetIndex: 2 },
    ]);
  });

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

  it('preserves opaque backend numbering metadata through editor round trips', () => {
    const source = '<!-- heading-numbering: {"ordered_style":"chinese"} -->\n# 标题\n\n<a id="block-sec-1" numbering="restart"></a>\n## 章节';
    const editorValue = writerMarkdownForEditor(source);
    const editableValue = writerMarkdownForEditing(source);
    const savedValue = writerMarkdownForSave(editorValue);
    expect(editorValue).toContain('numbering="restart"');
    expect(editableValue).not.toContain('heading-numbering');
    expect(writerMarkdownForSave(
      protectWriterMarkdownHeadingAnchors(source, editableValue),
    )).toBe(source);
    expect(savedValue).toBe(source);
  });

  it('keeps materialized roman-numbered headings visible in the editor', () => {
    const source = [
      '<!-- heading-numbering: {"ordered_style":"parenthesized"} -->',
      '# Title',
      '<a id="block-a"></a>',
      '## (1) A',
      '<a id="block-b"></a>',
      '### (a) B',
      '<a id="block-c"></a>',
      '#### (i) C',
    ].join('\n');

    const editable = writerMarkdownForEditing(source);
    expect(editable).toContain('#### (i) C');
    expect(editable).not.toContain('heading-numbering');
    expect(editable).not.toContain('<a id=');
  });

  it('keeps PDF page markers hidden and round-trippable through MDXEditor', () => {
    const source = [
      '<!-- 第 1 页 -->',
      '',
      '招标文件正文',
      '',
      '```html',
      '<!-- 第 2 页 -->',
      '```',
    ].join('\n');

    const editorValue = writerMarkdownForEditing(source);

    expect(editorValue).toContain('<a id="writer-page-marker-1" />');
    expect(editorValue).not.toContain('<!-- 第 1 页 -->');
    expect(editorValue).toContain('```html\n<!-- 第 2 页 -->\n```');
    expect(writerMarkdownForSave(editorValue)).toBe(source);
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
      '![插图](https://example.com/image.png)',
    ].join('\n'));
  });

  it('keeps image and following heading anchors stable across an HTML image round trip', () => {
    const source = [
      '# 标题',
      '',
      '<a id="block-sec-002-001"></a>',
      '### 证据与谜团',
      '',
      '[因果链](#block-IMAGE-1)',
      '',
      '<a id="block-IMAGE-1"></a>',
      '![恐惧递进因果链](/data/chain.jpg)',
      '',
      '<a id="block-sec-002-002"></a>',
      '### 不可名状的征兆',
    ].join('\n');
    const editable = writerMarkdownForEditing(source).replace(
      '![恐惧递进因果链](/data/chain.jpg)',
      '<img height="712" width="712" alt="恐惧递进因果链" src="/data/chain.jpg" />',
    );
    const restored = writerMarkdownForSave(
      protectWriterMarkdownHeadingAnchors(source, editable),
    );

    expect(editable).not.toContain('<a id=');
    expect(restored).toContain(
      '<a id="block-IMAGE-1"></a>\n<img height="712" width="712"',
    );
    expect(restored).toContain(
      '<a id="block-sec-002-002"></a>\n### 不可名状的征兆',
    );
    expect(restored).not.toContain('block-user-');
  });

  it('keeps outline instructions hidden from the editor and restores them on save', () => {
    const sidecar = '<!-- writer:outline {"node_id":"sec-1","target_chars":1200,"context_relations":[],"subtasks":[{"subtask_id":"st-1","subtask_type":"retrieve","question":"补充行业数据","status":"pending"}]} -->';
    const source = [
      '# 标题',
      '',
      '<a id="block-sec-1"></a>',
      '## 系统设计',
      sidecar,
    ].join('\n');

    const editable = writerMarkdownForEditing(source);
    expect(editable).not.toContain('writer:outline');
    expect(writerMarkdownForSave(
      protectWriterMarkdownHeadingAnchors(source, editable),
    )).toBe(source);
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

  it('preserves an image anchor when the editor serializes Markdown as HTML', () => {
    const source = [
      '<a id="block-image-1"></a>',
      '![灯塔](https://example.com/lighthouse.png)',
    ].join('\n');
    const edited = [
      '<img height="712" width="712" alt="灯塔" src="https://example.com/lighthouse.png" />',
    ].join('\n');
    const restored = protectWriterMarkdownHeadingAnchors(source, edited);

    expect(restored).toBe([
      '<a id="block-image-1" />',
      '<img height="712" width="712" alt="灯塔" src="https://example.com/lighthouse.png" />',
    ].join('\n'));
    expect(collectWriterMarkdownReferenceTargets(restored)).toEqual([
      { anchorId: 'block-image-1', label: '灯塔', type: 'image' },
    ]);
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

  it('does not promote the first section to a duplicate title after deleting the document heading', () => {
    const outline = collectWriterMarkdownOutline('<a id="block-sec-1" />\n## 1 理智的崩塌\n<a id="block-sec-2" />\n## 2 深渊');
    expect(outline.title).toBeUndefined();
    expect(outline.items.map((item) => item.label)).toEqual(['1 理智的崩塌', '2 深渊']);
  });

  it('collects hidden outline instructions for the outline rail', () => {
    const source = [
      '# 产品架构说明',
      '<a id="block-sec-1"></a>',
      '## 系统设计',
      '<!-- writer:outline {"node_id":"ignored","target_chars":900,"context_relations":[{"relation":"continuity","target_node_id":"sec-0","guidance":"承接背景"}],"subtasks":[{"subtask_id":"st-1","subtask_type":"reason","question":"比较两种方案","status":"pending"}]} -->',
    ].join('\n');

    expect(collectWriterMarkdownOutline(source).items[0]).toEqual({
      anchorId: 'block-sec-1',
      label: '系统设计',
      level: 2,
      instructions: {
        node_id: 'sec-1',
        target_chars: 900,
        context_relations: [{
          relation: 'continuity',
          target_node_id: 'sec-0',
          guidance: '承接背景',
        }],
        subtasks: [{
          subtask_id: 'st-1',
          subtask_type: 'reason',
          question: '比较两种方案',
          status: 'pending',
        }],
      },
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

  it('unwraps a selected reference by target when the paragraph contains inline formatting', () => {
    const source = '**重点**：详见[需求理解](#block-sec-1)。';

    expect(removeWriterMarkdownInternalReference(
      source,
      '重点：详见需求理解。',
      5,
      '需求理解',
      { anchorId: 'block-sec-1', occurrence: 0 },
    )).toBe('**重点**：详见需求理解。');
  });

  it('uses the selected reference occurrence when a target is linked more than once', () => {
    const source = [
      '[第一次](#block-sec-1)',
      '',
      '[第二次](#block-sec-1)',
    ].join('\n');

    expect(removeWriterMarkdownInternalReference(
      source,
      '第二次',
      0,
      '第二次',
      { anchorId: 'block-sec-1', occurrence: 1 },
    )).toBe('[第一次](#block-sec-1)\n\n第二次');
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
