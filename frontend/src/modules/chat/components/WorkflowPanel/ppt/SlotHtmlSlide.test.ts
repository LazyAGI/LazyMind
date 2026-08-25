import { describe, expect, it } from 'vitest';

import { dataElOccurrenceIndex, fitSlideFrame, pptClickedText } from './SlotHtmlSlide';

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
});
