import { afterEach, beforeAll, describe, expect, it } from 'vitest';
import css from './WorkflowPanel.scss?inline';
import progressCss from './PaperAnalysisProgress.scss?inline';
import runCss from '@/modules/chat/pages/workflowRun/index.scss?inline';

import promptCss from './PptPromptDocument.scss?inline';
import editorCss from './MarkdownArtifactEditor.scss?inline';

beforeAll(() => {
  const style = document.createElement('style');
  style.textContent = editorCss + promptCss + css + progressCss + runCss;
  document.head.appendChild(style);
});
afterEach(() => {
  document.body.replaceChildren();
  document.documentElement.classList.remove('workflow-run-embed');
});

function renderLongTab(layout: string, expanded = false) {
  const panel = document.createElement('section');
  panel.className = `workflow-panel${expanded ? ' workflow-panel--expanded' : ''}`;
  panel.innerHTML = `
    <div class="workflow-panel__topbar">Workflow</div>
    <div class="workflow-panel__trust-notice">Trust notice</div>
    <div class="workflow-panel__body">
      <div role="tabpanel">
        <div class="workflow-panel__tab-content workflow-panel__tab-content--${layout}">
          <div class="workflow-panel__named-slot">
            <div class="workflow-panel__slot-heading">Long material</div>
            <div class="workflow-slot workflow-slot--text">
              <p class="workflow-slot__text">${'完整内容\n'.repeat(200)}最后一行</p>
              <div class="workflow-slot__text-meta">Version controls</div>
            </div>
          </div>
          <div class="workflow-panel__named-slot">Last material</div>
        </div>
      </div>
    </div>
    <div class="workflow-panel__footer">Rollback actions</div>`;
  document.body.appendChild(panel);
  return panel;
}

function renderMarkdownDocument(parent: Element) {
  parent.innerHTML = `<div class="workflow-panel__document-item">
    <div class="workflow-slot workflow-slot--artifact">
      <div class="workflow-slot__artifact-body workflow-slot__artifact-body--markdown">
        <section class="writer-markdown-editor" style="--writer-markdown-max-height:200px">
          <div class="writer-markdown-editor__document-layout"><div class="writer-markdown-editor__main">
            <div class="writer-markdown-editor__surface"><div class="mdxeditor-root-contenteditable">
              <h1>完整标题</h1><p>${'正文内容。'.repeat(300)}</p><p>最后一行</p>
            </div></div>
          </div></div>
        </section>
      </div>
    </div>
  </div>`;
  return parent.querySelector<HTMLElement>('.writer-markdown-editor__surface')!;
}

describe('workflow material scrolling contract', () => {
  it.each([false, true])('keeps the hosted analysis timeline scrollable with expanded=%s', expanded => {
    document.documentElement.classList.add('workflow-run-embed');
    const panel = renderLongTab('list', expanded);
    panel.classList.add('workflow-panel--embedded');
    const run = document.createElement('main');
    run.className = 'workflow-run workflow-run--embedded';
    document.body.appendChild(run);
    run.appendChild(panel);
    const tab = panel.querySelector<HTMLElement>('[role="tabpanel"]')!;
    tab.classList.add('workflow-panel__f4-tab');
    tab.insertAdjacentHTML('afterbegin', '<article class="paper-f4-progress"><details open><summary>查看当前执行过程</summary><ol><li>最后一个执行阶段</li></ol></details></article>');
    expect(getComputedStyle(tab).overflow).toBe('hidden auto');
    expect(getComputedStyle(tab).overflowY).toBe('auto');
    expect(getComputedStyle(tab).overflowX).toBe('hidden');
    expect(getComputedStyle(tab).flexShrink).toBe('1');
    expect(getComputedStyle(panel.querySelector('.workflow-panel__tab-content')!).overflow).toBe('visible');
    expect(getComputedStyle(panel.querySelector('.workflow-panel__tab-content')!).flexGrow).toBe('0');
    expect(getComputedStyle(panel.querySelector('.workflow-panel__footer')!).flexShrink).toBe('0');
  });

  it.each([false, true])('bounds hosted tabs within the iframe viewport with embedded=%s', embedded => {
    document.documentElement.classList.toggle('workflow-run-embed', embedded);
    const panel = renderLongTab('list');
    panel.classList.add('workflow-panel--embedded');
    const run = document.createElement('main');
    run.className = 'workflow-run workflow-run--embedded';
    document.body.appendChild(run);
    run.appendChild(panel);
    const body = getComputedStyle(panel.querySelector('.workflow-panel__body')!);
    const tab = getComputedStyle(panel.querySelector('[role="tabpanel"]')!);
    expect(body.overflow).toBe(embedded ? 'hidden' : 'auto');
    expect(tab.overflow).toBe(embedded ? 'hidden' : 'auto');
    expect(tab.flexShrink).toBe(embedded ? '1' : '0');
  });

  it.each([false, true])('scrolls persisted Markdown with its tab even when the widget is text-markdown (expanded=%s)', expanded => {
    const panel = renderLongTab('list', expanded);
    const slot = panel.querySelector('.workflow-panel__named-slot')!;
    const surface = renderMarkdownDocument(slot);
    expect(getComputedStyle(surface).maxHeight).toBe('none');
    expect(getComputedStyle(surface).overflowY).toBe('visible');
    expect(getComputedStyle(slot.querySelector('.writer-markdown-editor')!).maxHeight).toBe('none');
    expect(getComputedStyle(panel.querySelector('.workflow-panel__tab-content')!).overflowY).toBe('auto');
  });

  it.each([false, true])('keeps a dedicated single-document workspace internally scrollable (expanded=%s)', expanded => {
    const panel = renderLongTab('list', expanded);
    const slots = panel.querySelectorAll('.workflow-panel__named-slot');
    slots[1].remove();
    slots[0].classList.add('workflow-panel__named-slot--writer-document');
    const surface = renderMarkdownDocument(slots[0]);
    expect(getComputedStyle(surface).overflowY).toBe('auto');
    expect(getComputedStyle(surface).maxHeight).toBe('none');
    expect(parseFloat(getComputedStyle(surface).minHeight)).toBe(0);
  });

  it.each([false, true])('scrolls multiple documents together rather than nesting editor viewports (expanded=%s)', expanded => {
    const panel = renderLongTab('list', expanded);
    for (const slot of panel.querySelectorAll('.workflow-panel__named-slot')) {
      slot.classList.add('workflow-panel__named-slot--writer-document');
      const surface = renderMarkdownDocument(slot);
      expect(getComputedStyle(surface).maxHeight).toBe('none');
      expect(getComputedStyle(surface).overflowY).toBe('visible');
      expect(getComputedStyle(slot).flexShrink).toBe('0');
    }
  });

  it.each(['', ' composite-grid__row--stack'])('uses one page scroller for long composite text (%s)', rowClass => {
    const panel = document.createElement('section');
    panel.className = 'workflow-panel';
    panel.innerHTML = `<div class="composite-main"><div class="composite-grid composite-grid--paged">
      <div class="composite-grid__row${rowClass}"><div class="composite-grid__cell"></div></div>
    </div></div>`;
    document.body.appendChild(panel);
    const cell = panel.querySelector('.composite-grid__cell')!;
    const surface = renderMarkdownDocument(cell);
    cell.insertAdjacentHTML('beforeend', '<div class="ppt-prompt-document__body" style="--workflow-ppt-prompt-max-height:420px">长提示词</div>');
    expect(getComputedStyle(panel.querySelector('.composite-main')!).overflow).toBe('hidden');
    expect(getComputedStyle(panel.querySelector('.composite-grid')!).overflow).toBe('auto');
    expect(getComputedStyle(surface).overflowY).toBe('visible');
    expect(getComputedStyle(surface).maxHeight).toBe('none');
    expect(getComputedStyle(cell.querySelector('.ppt-prompt-document__body')!).maxHeight).toBe('none');
    // Explicitly sized nested frames still own their scrolling.
    panel.querySelector('.composite-grid__row')!.className = 'composite-grid__row composite-grid__row--tree';
    expect(getComputedStyle(surface).overflowY).toBe('auto');
    expect(getComputedStyle(surface).maxHeight).toBe('480px');
  });
  it.each(['vertical', 'list', 'horizontal', 'grid'])('gives compact %s tabs a bounded scroll viewport instead of clipping long material', layout => {
    const panel = renderLongTab(layout);
    const viewport = panel.querySelector<HTMLElement>('.workflow-panel__tab-content')!;
    expect(getComputedStyle(viewport).overflowY).toBe('auto');
    expect(parseFloat(getComputedStyle(viewport).minHeight)).toBe(0);
    expect(getComputedStyle(panel.querySelector('.workflow-panel__footer')!).flexShrink).toBe('0');
    if (layout === 'vertical' || layout === 'list') {
      for (const material of viewport.querySelectorAll('.workflow-panel__named-slot')) {
        expect(getComputedStyle(material).flexShrink).toBe('0');
      }
    }
  });

  it.each([false, true])('lets the tab own default text scrolling (expanded=%s), keeping the last line and version controls in its content flow', expanded => {
    const panel = renderLongTab('vertical', expanded);
    const text = panel.querySelector<HTMLElement>('.workflow-slot__text')!;
    expect(getComputedStyle(text).maxHeight).toBe('none');
    expect(getComputedStyle(text).overflowY).toBe('visible');
    expect(text.textContent).toContain('最后一行');
    expect(panel.querySelector('.workflow-slot__text-meta')?.nextElementSibling).toBeNull();
  });

  it.each([false, true])('keeps explicit text heights out of the tab scroll flow (expanded=%s)', expanded => {
    const panel = renderLongTab('vertical', expanded);
    const text = panel.querySelector<HTMLElement>('.workflow-slot__text')!;
    text.style.setProperty('--workflow-text-max-height', '420px');
    expect(getComputedStyle(text).maxHeight).toBe('none');
    expect(getComputedStyle(text).overflowY).toBe('visible');
    const prompt = document.createElement('div');
    prompt.className = 'ppt-prompt-document__body';
    prompt.style.setProperty('--workflow-ppt-prompt-max-height', '420px');
    text.parentElement!.appendChild(prompt);
    expect(getComputedStyle(prompt).maxHeight).toBe('none');
    expect(getComputedStyle(prompt).overflowY).toBe('visible');
  });

  it('retains height constraints outside ordinary tab slots', () => {
    const panel = renderLongTab('vertical');
    panel.querySelector('.workflow-panel__tab-content')!.className = 'composite-grid';
    const text = panel.querySelector<HTMLElement>('.workflow-slot__text')!;
    text.style.setProperty('--workflow-text-max-height', '420px');
    expect(getComputedStyle(text).maxHeight).toBe('var(--workflow-text-max-height, 520px)');
    expect(getComputedStyle(text).overflowY).toBe('auto');
  });

  it.each([false, true])('does not apply ordinary-text overrides to document editor slots (expanded=%s)', expanded => {
    const panel = renderLongTab('list', expanded);
    const materials = panel.querySelectorAll('.workflow-panel__named-slot');
    materials[1].remove();
    materials[0].classList.add('workflow-panel__named-slot--writer-document');
    // Document slots have their own scrolling contract. Even a text descendant
    // must retain its existing constraints rather than receive this override.
    const text = materials[0].querySelector<HTMLElement>('.workflow-slot__text')!;
    expect(getComputedStyle(text).maxHeight).toBe('var(--workflow-text-max-height, 520px)');
    expect(getComputedStyle(text).overflowY).toBe('auto');
  });
});
