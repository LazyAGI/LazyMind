import { describe, expect, it, vi } from 'vitest';

import {
  createPptMultiSelectionOverlay,
  dataElOccurrenceIndex,
  fitSlideFrame,
  forwardSlideFrameWheel,
  markImplicitPptSelectionGroups,
  pptClickedText,
  pptDomPath,
  resolvePptHoverCandidate,
} from './SlotHtmlSlide';

function setScrollMetrics(
  element: HTMLElement,
  { clientHeight, scrollHeight, scrollTop = 0 }: {
    clientHeight: number;
    scrollHeight: number;
    scrollTop?: number;
  },
) {
  Object.defineProperty(element, 'clientHeight', { configurable: true, value: clientHeight });
  Object.defineProperty(element, 'scrollHeight', { configurable: true, value: scrollHeight });
  element.scrollTop = scrollTop;
}

describe('fitSlideFrame', () => {
  it('fits by width in a 16:9 material frame', () => {
    expect(fitSlideFrame(800, 450)).toEqual({ scale: 0.5, left: 0, top: 0 });
  });

  it('fits by height and centers the slide in a narrow-height frame', () => {
    expect(fitSlideFrame(800, 225)).toEqual({ scale: 0.25, left: 200, top: 0 });
  });

  it('fits by width and vertically centers the slide in a tall frame', () => {
    expect(fitSlideFrame(400, 450)).toEqual({ scale: 0.25, left: 0, top: 112.5 });
  });
});

describe('PPT HTML element selection', () => {
  it('records the clicked occurrence when data-el is duplicated', () => {
    document.body.innerHTML = `
      <div data-el="title">EYEBROW</div>
      <h1 data-el="title"><span>Main title</span></h1>
      <p data-el="subtitle">Subtitle</p>
    `;
    const titles = document.querySelectorAll<HTMLElement>('[data-el="title"]');
    const subtitle = document.querySelector<HTMLElement>('[data-el="subtitle"]');

    expect(dataElOccurrenceIndex(titles[0])).toBe(1);
    expect(dataElOccurrenceIndex(titles[1])).toBe(2);
    expect(dataElOccurrenceIndex(subtitle!)).toBe(1);
  });

  it('treats a styling span click as an edit of the whole heading text', () => {
    document.body.innerHTML = '<h1 data-el="title"><span>赛博朋克</span>2077</h1>';
    const title = document.querySelector<HTMLElement>('h1')!;
    const span = document.querySelector<HTMLElement>('span')!;

    expect(pptClickedText(title, span)).toBe('赛博朋克2077');
  });

  it('keeps a nested text target when data-el belongs to a larger section', () => {
    document.body.innerHTML = `
      <section data-el="section-1"><h2>核心玩法</h2><p>开放世界探索</p></section>
    `;
    const section = document.querySelector<HTMLElement>('section')!;
    const heading = document.querySelector<HTMLElement>('h2')!;

    expect(pptClickedText(section, heading)).toBe('核心玩法');
  });

  it('renders one union frame and count for a multi-selection', () => {
    document.body.innerHTML = `
      <div data-el="bullet-1">一</div>
      <div data-el="bullet-2">二</div>
      <div data-el="bullet-3">三</div>
    `;
    const targets = Array.from(document.querySelectorAll<HTMLElement>('[data-el]'));
    targets.forEach((target, index) => {
      const left = 100 + index * 240;
      vi.spyOn(target, 'getBoundingClientRect').mockReturnValue({
        x: left, y: 200, left, top: 200, right: left + 200, bottom: 400,
        width: 200, height: 200, toJSON: () => ({}),
      });
    });

    const overlay = createPptMultiSelectionOverlay(targets)!;

    expect(overlay).toHaveClass('lazymind-ppt-edit-multi-selection-overlay');
    expect(overlay).toHaveTextContent('已选 3 个元素');
    overlay.remove();
  });

  it('recognizes an old same-row flex family as an implicit group', () => {
    document.body.innerHTML = `
      <div id="row" style="display:flex">
        <div data-el="bullet-1">一</div>
        <div data-el="bullet-2">二</div>
        <div data-el="bullet-3">三</div>
      </div>
    `;
    const row = document.querySelector<HTMLElement>('#row')!;
    const targets = Array.from(row.querySelectorAll<HTMLElement>('[data-el]'));
    targets.forEach((target, index) => {
      const left = 100 + index * 220;
      vi.spyOn(target, 'getBoundingClientRect').mockReturnValue({
        x: left, y: 100, left, top: 100, right: left + 200, bottom: 300,
        width: 200, height: 200, toJSON: () => ({}),
      });
    });

    markImplicitPptSelectionGroups(document);

    expect(row).toHaveAttribute('data-lazymind-selection-group', 'true');
    expect(resolvePptHoverCandidate(row)?.targets).toEqual(targets);
  });

  it('selects all elements that share an explicit data-group', () => {
    document.body.innerHTML = `
      <section>
        <div data-el="card-1" data-group="cards">一</div>
        <div data-el="card-2" data-group="cards">二</div>
        <div data-el="footer">页脚</div>
      </section>
    `;
    const cards = Array.from(document.querySelectorAll<HTMLElement>('[data-group="cards"]'));

    expect(resolvePptHoverCandidate(cards[0])).toEqual({ targets: cards, scope: 'group' });
  });

  it('adds a stable DOM path target for an old visual without data-el', () => {
    document.body.innerHTML = `
      <main><h1 data-el="title">标题</h1><div id="art"><svg><text>X</text></svg></div></main>
    `;
    const art = document.querySelector<HTMLElement>('#art')!;
    const svg = document.querySelector<HTMLElement>('svg')!;
    vi.spyOn(svg, 'getBoundingClientRect').mockReturnValue({
      x: 800, y: 100, left: 800, top: 100, right: 1500, bottom: 700,
      width: 700, height: 600, toJSON: () => ({}),
    });
    vi.spyOn(art, 'getBoundingClientRect').mockReturnValue({
      x: 790, y: 90, left: 790, top: 90, right: 1510, bottom: 710,
      width: 720, height: 620, toJSON: () => ({}),
    });

    markImplicitPptSelectionGroups(document);

    expect(art).toHaveAttribute('data-lazymind-selection-target', 'visual');
    expect(art.dataset.lazymindSelectionEl).toMatch(/^__lazymind_auto_div_/);
    expect(pptDomPath(art)).toEqual([0, 1]);
    expect(resolvePptHoverCandidate(svg)).toEqual({ targets: [art], scope: 'item' });
  });
});

describe('PPT iframe wheel forwarding', () => {
  it('uses the nearest outer element that can scroll in the wheel direction', () => {
    const scrollOwner = document.createElement('div');
    const exhaustedScroller = document.createElement('div');
    const frame = document.createElement('iframe');
    scrollOwner.style.overflowY = 'auto';
    exhaustedScroller.style.overflowY = 'auto';
    scrollOwner.appendChild(exhaustedScroller);
    exhaustedScroller.appendChild(frame);
    document.body.appendChild(scrollOwner);
    setScrollMetrics(scrollOwner, { clientHeight: 200, scrollHeight: 600 });
    setScrollMetrics(exhaustedScroller, { clientHeight: 200, scrollHeight: 200 });
    const scrollBy = vi.fn();
    Object.defineProperty(scrollOwner, 'scrollBy', { configurable: true, value: scrollBy });
    const event = new WheelEvent('wheel', {
      cancelable: true,
      deltaY: 120,
    });

    expect(forwardSlideFrameWheel(frame, event)).toBe(true);
    expect(event.defaultPrevented).toBe(true);
    expect(scrollBy).toHaveBeenCalledWith({ top: 120, behavior: 'auto' });
  });

  it('forwards past an overflowing iframe root that explicitly hides overflow', () => {
    const scrollOwner = document.createElement('div');
    const frame = document.createElement('iframe');
    scrollOwner.style.overflowY = 'auto';
    scrollOwner.appendChild(frame);
    document.body.appendChild(scrollOwner);
    setScrollMetrics(scrollOwner, { clientHeight: 200, scrollHeight: 600 });
    const scrollBy = vi.fn();
    Object.defineProperty(scrollOwner, 'scrollBy', { configurable: true, value: scrollBy });
    const frameRoot = frame.contentDocument!.documentElement;
    frameRoot.style.overflowY = 'hidden';
    setScrollMetrics(frameRoot, { clientHeight: 900, scrollHeight: 1200 });
    const event = new WheelEvent('wheel', {
      cancelable: true,
      deltaY: 120,
    });

    expect(forwardSlideFrameWheel(frame, event)).toBe(true);
    expect(event.defaultPrevented).toBe(true);
    expect(scrollBy).toHaveBeenCalledWith({ top: 120, behavior: 'auto' });
  });

  it('keeps wheel input inside an iframe element that can still scroll', () => {
    const scrollOwner = document.createElement('div');
    const frame = document.createElement('iframe');
    scrollOwner.style.overflowY = 'auto';
    scrollOwner.appendChild(frame);
    document.body.appendChild(scrollOwner);
    setScrollMetrics(scrollOwner, { clientHeight: 200, scrollHeight: 600 });
    const scrollBy = vi.fn();
    Object.defineProperty(scrollOwner, 'scrollBy', { configurable: true, value: scrollBy });

    const innerScroller = frame.contentDocument!.createElement('div');
    innerScroller.style.overflowY = 'auto';
    frame.contentDocument!.body.appendChild(innerScroller);
    setScrollMetrics(innerScroller, { clientHeight: 100, scrollHeight: 300 });
    let forwarded = true;
    innerScroller.addEventListener('wheel', (event) => {
      forwarded = forwardSlideFrameWheel(frame, event);
    });
    const event = new WheelEvent('wheel', {
      bubbles: true,
      cancelable: true,
      deltaY: 80,
    });
    innerScroller.dispatchEvent(event);

    expect(forwarded).toBe(false);
    expect(event.defaultPrevented).toBe(false);
    expect(scrollBy).not.toHaveBeenCalled();

    innerScroller.scrollTop = 200;
    const boundaryEvent = new WheelEvent('wheel', {
      bubbles: true,
      cancelable: true,
      deltaY: 80,
    });
    innerScroller.dispatchEvent(boundaryEvent);

    expect(forwarded).toBe(true);
    expect(boundaryEvent.defaultPrevented).toBe(true);
    expect(scrollBy).toHaveBeenCalledWith({ top: 80, behavior: 'auto' });

    scrollBy.mockClear();
    innerScroller.scrollTop = 100;
    const upwardEvent = new WheelEvent('wheel', {
      bubbles: true,
      cancelable: true,
      deltaY: -80,
    });
    innerScroller.dispatchEvent(upwardEvent);

    expect(forwarded).toBe(false);
    expect(upwardEvent.defaultPrevented).toBe(false);
    expect(scrollBy).not.toHaveBeenCalled();
  });

  it('does not scroll the page behind the zoomed slide', () => {
    const scrollOwner = document.createElement('div');
    const frame = document.createElement('iframe');
    frame.classList.add('slot-html-slide__frame--zoomed');
    scrollOwner.style.overflowY = 'auto';
    scrollOwner.appendChild(frame);
    document.body.appendChild(scrollOwner);
    setScrollMetrics(scrollOwner, { clientHeight: 200, scrollHeight: 600 });
    const scrollBy = vi.fn();
    Object.defineProperty(scrollOwner, 'scrollBy', { configurable: true, value: scrollBy });
    const event = new WheelEvent('wheel', {
      cancelable: true,
      deltaY: 120,
    });

    expect(forwardSlideFrameWheel(frame, event)).toBe(false);
    expect(event.defaultPrevented).toBe(false);
    expect(scrollBy).not.toHaveBeenCalled();
  });

  it('forwards wheel input inside an expanded workflow panel', () => {
    const panel = document.createElement('div');
    const scrollOwner = document.createElement('div');
    const frame = document.createElement('iframe');
    panel.classList.add('workflow-panel--expanded');
    scrollOwner.style.overflowY = 'auto';
    panel.appendChild(scrollOwner);
    scrollOwner.appendChild(frame);
    document.body.appendChild(panel);
    setScrollMetrics(scrollOwner, { clientHeight: 200, scrollHeight: 600 });
    const scrollBy = vi.fn();
    Object.defineProperty(scrollOwner, 'scrollBy', { configurable: true, value: scrollBy });
    const event = new WheelEvent('wheel', {
      cancelable: true,
      deltaY: 120,
    });

    expect(forwardSlideFrameWheel(frame, event)).toBe(true);
    expect(event.defaultPrevented).toBe(true);
    expect(scrollBy).toHaveBeenCalledWith({ top: 120, behavior: 'auto' });
  });
});
