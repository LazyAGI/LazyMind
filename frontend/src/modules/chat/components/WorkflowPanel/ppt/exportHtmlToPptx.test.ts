import { describe, expect, it, vi } from 'vitest';

import {
  applyHtmlPreviewCompatibilityFallbacks,
  htmlForStaticPreview,
  htmlForRasterCapture,
  inlineSvgStylesForRasterCapture,
  waitForAnimationFrames,
} from './exportHtmlToPptx';

describe('PPT HTML preview compatibility', () => {
  it('keeps class-styled SVG text and shapes styled after their document CSS is removed', () => {
    document.body.innerHTML = `<style>
      .node { fill: rgb(7, 13, 24); stroke: rgb(45, 212, 191); }
      .label { fill: rgb(230, 237, 247); font-size: 11px; font-family: Arial; font-weight: 700; }
    </style><svg><rect class="node"/><text class="label">M 维 = 16</text></svg>`;
    inlineSvgStylesForRasterCapture(document);
    document.querySelector('style')!.remove();
    const shape = document.querySelector<SVGElement>('rect')!;
    const label = document.querySelector<SVGElement>('text')!;
    expect(shape.style.fill).toBe('rgb(7, 13, 24)');
    expect(shape.style.stroke).toBe('rgb(45, 212, 191)');
    expect(label.style.fill).toBe('rgb(230, 237, 247)');
    expect(label.style.fontSize).toBe('11px');
    expect(label.style.fontFamily).toBe('Arial');
    expect(label.textContent).toBe('M 维 = 16');
    document.body.innerHTML = '';
  });

  it('keeps authored font families and image fit in raster preparation', () => {
    const prepared = htmlForRasterCapture('<html><head><style>body{font-family:Georgia}img{object-fit:contain}</style></head><body></body></html>');
    expect(prepared).toContain('font-family:Georgia');
    expect(prepared).not.toContain('font-family:-apple-system');
    expect(prepared).not.toContain('img{object-fit:cover;}');
  });

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


it('does not hang raster exports when a hidden iframe never dispatches animation frames', async () => {
  vi.useFakeTimers();
  try {
    const hiddenWindow = {requestAnimationFrame: vi.fn((_callback: FrameRequestCallback) => 7), cancelAnimationFrame: vi.fn()};
    const finished = waitForAnimationFrames(hiddenWindow, 2);
    await vi.advanceTimersByTimeAsync(130);
    await finished;
    expect(hiddenWindow.requestAnimationFrame).toHaveBeenCalledTimes(2);
    expect(hiddenWindow.cancelAnimationFrame).toHaveBeenCalledTimes(2);
    expect(vi.getTimerCount()).toBe(0);
  } finally { vi.useRealTimers(); }
});


it('does not hide the rasterizer SVG wrapper with slide compatibility CSS', () => {
  const prepared = htmlForRasterCapture('<html><head></head><body><h1>Slide</h1></body></html>');
  const svg = new DOMParser().parseFromString(
    '<svg xmlns="http://www.w3.org/2000/svg"><foreignObject id="capture"><body xmlns="http://www.w3.org/1999/xhtml"><svg xmlns="http://www.w3.org/2000/svg"><foreignObject id="source" /></svg></body></foreignObject></svg>',
    'image/svg+xml',
  );
  const rules = [...prepared.matchAll(/([^{}>]+)\{opacity:0!important;visibility:hidden!important;background:transparent!important;\}/g)];
  expect(rules).toHaveLength(2);
  for (const rule of rules) {
    expect(svg.querySelector('#capture')!.matches(rule[1])).toBe(false);
    expect(svg.querySelector('#source')!.matches(rule[1])).toBe(true);
  }
});
