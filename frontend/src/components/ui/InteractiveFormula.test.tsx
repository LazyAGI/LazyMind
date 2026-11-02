import {fireEvent,render,screen} from '@testing-library/react';
import {beforeAll,describe,it,expect,vi} from 'vitest';
import i18n from '@/i18n';
import katex from 'katex';
import {InteractiveFormula} from './InteractiveFormula';
beforeAll(async()=>{await i18n.changeLanguage('en-US');});
const definitions=[{symbol:String.raw`\hat{\mathbf{x}}_i`,meaning:'Feature vector',evidence_ids:['e'],inferred:true}];
const evidence=[{id:'e',text:'Exact source definition.',page:2,bbox:[0,0,1,.1] as [number,number,number,number]}];
describe('symbols inside complete formulas',()=>{
 it('preserves malformed extracted LaTeX without crashing the reader',()=>{
 const latex='x\\';
 const view=render(<InteractiveFormula latex={latex} sourceKey="malformed" definitions={[]} evidence={[]} onNavigate={vi.fn()}/>);
 expect(Array.from(view.container.querySelectorAll('pre')).some(node=>node.textContent===latex)).toBe(true);
 expect(screen.getByText('Interactive symbols are unavailable for this formula syntax. The original LaTeX is preserved.')).toBeInTheDocument();
 expect(screen.queryAllByRole('button',{name:/Symbol:/})).toHaveLength(0);
 });
 it('selects actual rendered symbols by click or keyboard and exposes exact evidence',()=>{
 const navigate=vi.fn();
 const view=render(<InteractiveFormula latex={String.raw`\hat {\mathbf {x}} _ {i} + y = 1`} sourceKey="source" definitions={definitions} evidence={evidence} onNavigate={navigate}/>);
 const symbols=screen.getAllByRole('button',{name:/Symbol:/});
 expect(symbols).toHaveLength(3);
 expect(view.container.querySelector('.pdf-interactive-equation')?.textContent).toContain('+');
 fireEvent.click(symbols[0]);
 expect(screen.getByText('Feature vector')).toBeInTheDocument();
 expect(screen.getByText('Exact source definition.')).toBeInTheDocument();
 fireEvent.click(screen.getByRole('button',{name:'Source · p. 3'}));
 expect(navigate).toHaveBeenCalledWith(evidence[0]);
 fireEvent.keyDown(symbols[1],{key:'Enter'});
 expect(screen.getByText('No verified definition is available for this symbol.')).toBeInTheDocument();
 expect(symbols[1]).toHaveAttribute('aria-pressed','true');
 view.rerender(<InteractiveFormula latex="z=2" sourceKey="other" definitions={definitions} evidence={evidence} onNavigate={navigate}/>);
 expect(screen.queryByText('Feature vector')).toBeNull();
 expect(screen.queryByRole('region',{name:'Selected symbol'})).toBeNull();
 });
 it('never trusts model-provided HTML classes or links',()=>{
 const view=render(<InteractiveFormula latex={String.raw`\htmlClass{pdf-symbol-0}{x}+\href{javascript:alert(1)}{y}`} sourceKey="unsafe" definitions={definitions} evidence={[]} onNavigate={vi.fn()}/>);
 expect(view.container.querySelector('a')).toBeNull();
 expect(view.container.querySelector('.pdf-symbol-0')).toBeNull();
 expect(screen.queryAllByRole('button',{name:/Symbol:/})).toHaveLength(0);
 });
});

it('renders the actual Text CC array intact and selects each observed CrossAttn call independently',()=>{
 const latex=String.raw`\begin{array}{c} \mathcal {L} _ {c c} = \sum_ {i = 1} ^ {l} \operatorname{ReLU} \big (C r o s s A t t n (I, T _ {N P _ {i}}) \\ - C r o s s A t t n (I, N P _ {i}) - \epsilon \big) \end{array}`;
 const defs=[{symbol:'CrossAttn(I,T_{NP_i})',meaning:'Contextual attention',evidence_ids:[],inferred:true},{symbol:'CrossAttn(I,NP_i)',meaning:'Isolated attention',evidence_ids:[],inferred:true}];
 const view=render(<InteractiveFormula latex={latex} sourceKey="cc" definitions={defs} evidence={[]} onNavigate={vi.fn()}/>);
 const calls=screen.getAllByRole('button',{name:'Symbol: C r o s s A t t n'});
 expect(calls).toHaveLength(2);
 expect(view.container.querySelector('.pdf-interactive-equation .katex-display')).not.toBeNull();
 fireEvent.click(calls[0]);expect(screen.getByText('Contextual attention')).toBeInTheDocument();
 fireEvent.click(calls[1]);expect(screen.getByText('Isolated attention')).toBeInTheDocument();
 expect(screen.queryByText('Contextual attention')).toBeNull();
});


it('shows missing symbol explanations immediately without starting another lookup',()=>{
 render(<InteractiveFormula latex="x+y" sourceKey="precomputed" definitions={[{symbol:'x',meaning:'Known input',evidence_ids:[],inferred:true}]} evidence={[]} onNavigate={vi.fn()}/>);
 fireEvent.click(screen.getByRole('button',{name:'Symbol: y'}));
 expect(screen.getByText('No verified definition is available for this symbol.')).toBeInTheDocument();
 expect(screen.queryByRole('button',{name:'Retry symbol lookup'})).toBeNull();
});

it('keeps symbol evidence compact in the source popup while retaining the jump',()=>{
 const navigate=vi.fn();
 render(<InteractiveFormula latex="x+y" sourceKey="compact" definitions={[{symbol:'x',meaning:'Known input',evidence_ids:['e'],inferred:false}]} evidence={evidence} compactEvidence onNavigate={navigate}/>);
 fireEvent.click(screen.getByRole('button',{name:'Symbol: x'}));
 const detail=screen.getByRole('region',{name:'Selected symbol'});
 expect(detail.querySelector('blockquote')).toBeNull();
 fireEvent.click(screen.getByRole('button',{name:'Source · p. 3'}));
 expect(navigate).toHaveBeenCalledWith(evidence[0]);
});


it('keeps real CC contrastive loss visually intact with independently operable exponent symbols',()=>{
 const latex="\\mathcal {L} _ {c o n t} = - \\log \\frac {e ^ {\\mathrm{sim} (r _ {n} ^ {q} , r _ {n} ^ {t}) / \\tau}}{e ^ {\\mathrm{sim} (r _ {n} ^ {q} , r _ {n} ^ {q -}) / \\tau} + \\sum_ {i = 1} ^ {N} e ^ {\\mathrm{sim} (r _ {n} ^ {q} , r _ {i} ^ {t}) / \\tau}}";
 const defs=[{symbol:String.raw`\tau`,meaning:'Source temperature definition',evidence_ids:[],inferred:true}];
 const view=render(<InteractiveFormula latex={latex} sourceKey="cc-contrastive" definitions={defs} evidence={[]} onNavigate={vi.fn()}/>);
 expect(screen.getAllByRole('button',{name:'Symbol: e'})).toHaveLength(3);
 const temperatures=screen.getAllByRole('button',{name:String.raw`Symbol: \tau`});expect(temperatures).toHaveLength(3);
 expect(screen.getAllByRole('button',{name:'Symbol: r _ {n} ^ {q}'})).toHaveLength(3);
 const expected=document.createElement('div');expected.innerHTML=katex.renderToString(latex,{displayMode:true,trust:false});
 expect(view.container.querySelector('.pdf-interactive-equation .katex-html')?.textContent).toBe(expected.querySelector('.katex-html')?.textContent);
 expect(view.container.querySelector('.pdf-interactive-equation [role="button"] [role="button"]')).toBeNull();
 fireEvent.keyDown(temperatures[0],{key:'Enter'});expect(screen.getByText('Source temperature definition')).toBeInTheDocument();
});
