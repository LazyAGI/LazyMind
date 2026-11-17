import {expect,it} from 'vitest';
import {createPdfScrollSync} from './pdfScrollSync';

function viewport(height: number, visible = 200) {
  const element = document.createElement('div');
  Object.defineProperties(element,{scrollHeight:{value:height},clientHeight:{value:visible}});
  return element;
}
it('links both directions across different lengths and ignores programmatic echoes',()=>{
  const sync=createPdfScrollSync(),source=viewport(1200),translation=viewport(2200);
  sync.source.connect(source);sync.translation.connect(translation);
  source.scrollTop=250;sync.source.onScroll();expect(translation.scrollTop).toBe(0);
  sync.setEnabled(true);expect(translation.scrollTop).toBe(500);
  sync.translation.onScroll();expect(source.scrollTop).toBe(250);
  translation.scrollTop=1500;sync.translation.onScroll();expect(source.scrollTop).toBe(750);
  sync.source.onScroll();expect(translation.scrollTop).toBe(1500);
  sync.setEnabled(false);source.scrollTop=100;sync.source.onScroll();expect(translation.scrollTop).toBe(1500);
});
it('reconnects a loaded translation at the source position without retaining old viewers',()=>{
  const sync=createPdfScrollSync(),source=viewport(1200),first=viewport(2200),next=viewport(4200);
  sync.source.connect(source);const disconnect=sync.translation.connect(first);
  source.scrollTop=500;sync.setEnabled(true);disconnect();
  source.scrollTop=750;sync.source.onScroll();expect(first.scrollTop).toBe(1000);
  sync.translation.connect(next);expect(next.scrollTop).toBe(3000);
});

it.each(['source','translation'] as const)('does not propagate a %s layout correction but keeps later scrolling linked',side=>{
  const sync=createPdfScrollSync(),source=viewport(1200),translation=viewport(2200);
  sync.source.connect(source);sync.translation.connect(translation);
  source.scrollTop=250;sync.setEnabled(true);sync.translation.onScroll();
  const changed=side==='source'?source:translation;
  const other=side==='source'?translation:source;
  const previous=other.scrollTop;
  changed.scrollTop+=100;
  sync[side].onLayoutChange?.();
  sync[side].onScroll();
  expect(other.scrollTop).toBe(previous);

  changed.scrollTop+=100;
  sync[side].onScroll();
  expect(other.scrollTop).toBe(side==='source'?900:350);
});

it('keeps a user scroll that arrives before the layout correction echo',()=>{
  const sync=createPdfScrollSync(),source=viewport(1200),translation=viewport(2200);
  sync.source.connect(source);sync.translation.connect(translation);
  source.scrollTop=250;sync.setEnabled(true);sync.translation.onScroll();
  translation.scrollTop=600;
  sync.translation.onLayoutChange?.();
  translation.scrollTop=650;
  sync.translation.onScroll();
  expect(source.scrollTop).toBe(325);
});
