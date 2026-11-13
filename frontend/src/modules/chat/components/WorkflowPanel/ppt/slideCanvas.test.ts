import { afterEach, describe, expect, it, vi } from 'vitest';
import { DEFAULT_SLIDE_CANVAS, measureSlideCanvas } from './slideCanvas';

let frame: HTMLIFrameElement;
function slideDocument(html: string): Document {
  frame = document.createElement('iframe');
  document.body.appendChild(frame);
  const doc = frame.contentDocument!;
  doc.open();
  doc.write(html);
  doc.close();
  return doc;
}

afterEach(() => { frame?.remove(); vi.restoreAllMocks(); });

describe('slide canvas dimensions', () => {
  it('recognizes the document canvas in fixed-size HTML previews without a wrapper', () => {
    const doc = slideDocument(`<html><head><style>
      html,body{width:1280px;height:720px;margin:0;overflow:hidden}
      img{position:absolute;left:0;top:0;width:1280px;height:720px}
      </style></head><body><div class="background"></div><img alt="Slide preview"></body></html>`);
    expect(measureSlideCanvas(doc)).toEqual({ width: 1280, height: 720 });
  });

  it('accepts explicitly sized body or html and keeps a slide root authoritative', () => {
    const doc = slideDocument('<html style="width:1920px;height:1080px"><body style="width:1280px;height:720px"></body></html>');
    expect(measureSlideCanvas(doc)).toEqual({ width: 1280, height: 720 });
    doc.body.removeAttribute('style');
    expect(measureSlideCanvas(doc)).toEqual({ width: 1920, height: 1080 });
    doc.body.innerHTML = '<div class="wrapper" style="width:1600px;height:900px">Native slide</div>';
    expect(measureSlideCanvas(doc)).toEqual(DEFAULT_SLIDE_CANVAS);
  });

  it.each(['100%', '100vw', 'auto'])('does not mistake %s body dimensions for fixed pixels', (width) => {
    const doc = slideDocument(`<html><body style="width:${width};height:${width}">Content</body></html>`);
    // A browser resolves these to used pixels, unlike jsdom's absent layout.
    vi.spyOn(doc.defaultView!, 'getComputedStyle').mockReturnValue({ width: '1280px', height: '720px' } as CSSStyleDeclaration);
    Object.defineProperties(doc.body, { offsetWidth: { value: 1280 }, offsetHeight: { value: 720 } });
    expect(measureSlideCanvas(doc)).toEqual(DEFAULT_SLIDE_CANVAS);
  });

  it('keeps unsized content on the default canvas even when it happens to fill the viewport', () => {
    const doc = slideDocument('<html><body><p>A document without a declared canvas.</p></body></html>');
    vi.spyOn(doc.defaultView!, 'getComputedStyle').mockReturnValue({ width: '1280px', height: '720px' } as CSSStyleDeclaration);
    expect(measureSlideCanvas(doc)).toEqual(DEFAULT_SLIDE_CANVAS);
  });

  it('does not feed overridden relative document dimensions back into the viewport', () => {
    const doc = slideDocument('<html><head><style>body{width:1280px;height:720px}body{width:100%;height:100%}</style></head><body></body></html>');
    vi.spyOn(doc.defaultView!, 'getComputedStyle').mockReturnValue({ width: '1280px', height: '720px' } as CSSStyleDeclaration);
    expect(measureSlideCanvas(doc)).toEqual(DEFAULT_SLIDE_CANVAS);
  });

  it('preserves a native responsive root using its rendered dimensions, not percentage numbers', () => {
    const doc = slideDocument('<html><body><div class="wrapper" style="width:100%;height:100%">Native slide</div></body></html>');
    expect(measureSlideCanvas(doc)).toEqual(DEFAULT_SLIDE_CANVAS);
    Object.defineProperties(doc.querySelector('.wrapper')!, {
      offsetWidth: { value: 1600 }, offsetHeight: { value: 900 },
    });
    expect(measureSlideCanvas(doc)).toEqual(DEFAULT_SLIDE_CANVAS);
  });

  it('includes an explicitly sized body border box in the canvas', () => {
    const doc = slideDocument('<html><body style="width:1260px;height:700px;padding:10px"></body></html>');
    Object.defineProperties(doc.body, { offsetWidth: { value: 1280 }, offsetHeight: { value: 720 } });
    expect(measureSlideCanvas(doc)).toEqual({ width: 1280, height: 720 });
  });
});
