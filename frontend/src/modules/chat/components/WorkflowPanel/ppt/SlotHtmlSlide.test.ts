import { describe, expect, it } from 'vitest';

import { fitSlideFrame } from './SlotHtmlSlide';

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
