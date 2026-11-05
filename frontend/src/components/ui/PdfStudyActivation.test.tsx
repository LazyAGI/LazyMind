import {act,fireEvent,render} from '@testing-library/react';
import {afterEach,expect,it,vi} from 'vitest';
import {PdfStudyActivation} from './PdfStudyActivation';
afterEach(()=>{vi.useRealTimers();vi.unstubAllGlobals();});
function setup(){
 const disconnect=vi.fn();
 vi.stubGlobal('ResizeObserver',class{observe(){}disconnect=disconnect;});
 const element=document.createElement('div');
 element.getBoundingClientRect=()=>new DOMRect(40,20,800,600);
 return {frame:{current:element},disconnect};
}
it('places a decorative frame over the reading pane and removes it when animation finishes',()=>{
 const {frame,disconnect}=setup();render(<PdfStudyActivation frame={frame}/>);
 const pulse=document.querySelector('.pdf-study-activation')!;
 expect(pulse).toHaveAttribute('aria-hidden','true');
 expect(pulse).toHaveStyle({left:'40px',top:'20px',width:'800px',height:'600px'});
 fireEvent.animationEnd(pulse);
 // jsdom's feature detection may select React's WebKit event alias.
 fireEvent(pulse,new Event('webkitAnimationEnd',{bubbles:true}));
 expect(document.querySelector('.pdf-study-activation')).toBeNull();expect(disconnect).toHaveBeenCalled();
});
it('expires without an animation event and can replay on another activation',()=>{
 vi.useFakeTimers();const {frame}=setup();const view=render(<PdfStudyActivation key={1} frame={frame}/>);
 act(()=>vi.advanceTimersByTime(2200));expect(document.querySelector('.pdf-study-activation')).toBeNull();
 view.rerender(<PdfStudyActivation key={2} frame={frame}/>);expect(document.querySelector('.pdf-study-activation')).toBeInTheDocument();
 view.unmount();expect(document.querySelector('.pdf-study-activation')).toBeNull();
});
