import { describe, expect, it, vi } from 'vitest';

import {
  applyHtmlPreviewCompatibilityFallbacks,
  htmlForStaticPreview,
} from './exportHtmlToPptx';

describe('PPT HTML preview compatibility', () => {
  it('injects stable compositing fallbacks into preview HTML once', () => {
    const html = '<!doctype html><html><head></head><body><div class="wrapper"></div></body></html>';
    const once = htmlForStaticPreview(html);
    const twice = htmlForStaticPreview(once);

    expect(once).toContain('backdrop-filter:none!important');
    expect(once).toContain('mix-blend-mode:normal!important');
    expect(once).toContain('svg foreignObject');
    expect(once).toContain('[data-lazymind-unsupported-visual]');
    expect(twice.match(/data-lazymind-preview-static>/g)).toHaveLength(1);
    expect(twice.match(/data-lazymind-preview-static-tail>/g)).toHaveLength(1);
  });

  it('suppresses embedded renderers and non-2d canvases without reflowing layout', () => {
    document.body.innerHTML = `
      <video id="video"></video>
      <iframe id="nested"></iframe>
      <svg><foreignObject id="foreign"></foreignObject></svg>
      <canvas id="webgl"></canvas>
    `;
    const canvas = document.querySelector<HTMLCanvasElement>('#webgl')!;
    Object.defineProperty(canvas, 'getContext', {
      configurable: true,
      value: vi.fn(() => null),
    });

    expect(applyHtmlPreviewCompatibilityFallbacks(document)).toBe(4);
    for (const id of ['video', 'nested', 'foreign', 'webgl']) {
      const element = document.querySelector<HTMLElement | SVGElement>(`#${id}`)!;
      expect(element.getAttribute('data-lazymind-unsupported-visual')).toBeTruthy();
      expect(element.style.getPropertyValue('visibility')).toBe('hidden');
    }
    expect(applyHtmlPreviewCompatibilityFallbacks(document)).toBe(0);
  });

  it('keeps serializable 2d canvases available for ordinary decorations', () => {
    document.body.innerHTML = '<canvas id="decoration"></canvas>';
    const canvas = document.querySelector<HTMLCanvasElement>('#decoration')!;
    Object.defineProperty(canvas, 'getContext', {
      configurable: true,
      value: vi.fn(() => ({})),
    });
    Object.defineProperty(canvas, 'toDataURL', {
      configurable: true,
      value: vi.fn(() => 'data:image/png;base64,AAAA'),
    });

    expect(applyHtmlPreviewCompatibilityFallbacks(document)).toBe(0);
    expect(canvas.hasAttribute('data-lazymind-unsupported-visual')).toBe(false);
  });
});
