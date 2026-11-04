import {useFormulaExplanation} from '@/modules/knowledge/api/formulaExplanation';
import {act, fireEvent, renderHook, render, screen, waitFor} from '@testing-library/react';
import {beforeAll, beforeEach, describe, expect, it, vi} from 'vitest';
import i18n from '@/i18n';
import {PdfFormulaStudy} from './PdfFormulaStudy';
import {PdfStudyDialog} from './PdfStudyDialog';
import {CHAT_MODEL_SELECTION_CHANGED_EVENT} from '@/modules/chat/constants/chat';
import {AgentAppsAuth,AUTH_USER_CHANGE_EVENT} from '@/components/auth';
const mocks=vi.hoisted(()=>({post:vi.fn()}));
vi.mock('@/components/request',()=>({axiosInstance:mocks,BASE_URL:''}));
const block={id:'f',type:'formula',page:2,latex:'x^2'};
const explanation={summary:'Summary',symbols:[{symbol:'x',meaning:'Input',evidence_ids:['e'],inferred:false}],steps:[],evidence:[{id:'e',text:'Exact source quote',page:1,bbox:[0,0,1,.1]}],warnings:[]};
beforeAll(async()=>{await i18n.changeLanguage('en-US');});
beforeEach(()=>window.dispatchEvent(new Event(AUTH_USER_CHANGE_EVENT)));
describe('formula study panel',()=>{
 it('reuses a generated formula when its popup is reopened',async()=>{
  mocks.post.mockReset().mockResolvedValue({data:{data:explanation}});
  const props={target:{dataset:'d',document:'doc'},sourceKey:'memory-remount-cache',blocks:[block],selected:block,autoExplain:true,onSelect:vi.fn(),onNavigate:vi.fn(),onReturn:vi.fn(),canReturn:false,onClose:vi.fn()};
  const first=render(<PdfFormulaStudy {...props}/>);
  expect(await screen.findByText('Summary')).toBeInTheDocument();
  first.unmount();
  render(<PdfFormulaStudy {...props}/>);
  expect(await screen.findByText('Summary')).toBeInTheDocument();
  expect(mocks.post).toHaveBeenCalledTimes(1);
 });
 it('lays out a detailed formula reading as two paragraphs',async()=>{
  mocks.post.mockReset().mockResolvedValue({data:{data:{...explanation,summary:'The equation computes a weighted result.\n\nThe normalization controls how strongly each input contributes.'}}});
  const view=render(<PdfFormulaStudy target={{dataset:'d',document:'doc'}} sourceKey="two-paragraph-reading" blocks={[block]} selected={block} autoExplain onSelect={vi.fn()} onNavigate={vi.fn()} onReturn={vi.fn()} canReturn={false} onClose={vi.fn()}/>);
  expect(await screen.findByText('The equation computes a weighted result.')).toBeInTheDocument();
  expect(Array.from(view.container.querySelectorAll('.pdf-study-summary p'),paragraph=>paragraph.textContent)).toEqual([
   'The equation computes a weighted result.',
   'The normalization controls how strongly each input contributes.',
  ]);
 });
 it.each([CHAT_MODEL_SELECTION_CHANGED_EVENT,AUTH_USER_CHANGE_EVENT])('preserves model-independent results but clears user-specific cache after %s',async eventName=>{
  mocks.post.mockReset().mockResolvedValue({data:{data:explanation}});
  const props={target:{dataset:'d',document:'doc'},sourceKey:`invalidation-${eventName}`,blocks:[block],selected:block,autoExplain:true,onSelect:vi.fn(),onNavigate:vi.fn(),onReturn:vi.fn(),canReturn:false,onClose:vi.fn()};
  const first=render(<PdfFormulaStudy {...props}/>);
  expect(await screen.findByText('Summary')).toBeInTheDocument();
  first.unmount();
  act(()=>window.dispatchEvent(new Event(eventName)));
  render(<PdfFormulaStudy {...props}/>);
  await waitFor(()=>expect(mocks.post).toHaveBeenCalledTimes(eventName===AUTH_USER_CHANGE_EVENT?2:1));
 });
 it('makes every parsed term clickable in the source popup while keeping the full symbol list folded',async()=>{
  mocks.post.mockResolvedValue({data:{data:explanation}});
  render(<PdfStudyDialog onClose={vi.fn()}><PdfFormulaStudy target={{dataset:'d',document:'doc'}} sourceKey="hash" blocks={[block]} selected={{...block,latex:'x+y'}} autoExplain compact onSelect={vi.fn()} onNavigate={vi.fn()} onReturn={vi.fn()} canReturn={false} onClose={vi.fn()}/></PdfStudyDialog>);
  expect(await screen.findByText('Summary')).toBeInTheDocument();
  const symbols=screen.getAllByRole('button',{name:/Symbol:/});
  expect(symbols).toHaveLength(3);
  fireEvent.click(symbols[0]);
  expect(screen.getByRole('region',{name:'Selected symbol'})).toHaveTextContent('Input');
  expect(screen.getByText(/Symbols & meanings/).closest('details')).not.toHaveAttribute('open');
  expect(screen.queryByRole('button',{name:'Locate original formula'})).toBeNull();
  expect(screen.getByText('Summary').compareDocumentPosition(screen.getByText('Exact source quote')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
 });
 it('shows the detailed reading after the concise formula purpose',async()=>{
  mocks.post.mockReset().mockResolvedValue({data:{data:{...explanation,summary:'The loss pulls matching embeddings together.',reading:'It compares a query with its positive example and competing negatives. Optimizing it increases the positive score relative to the negatives.'}}});
  const view=render(<PdfFormulaStudy target={{dataset:'d',document:'doc'}} sourceKey="separate-reading" blocks={[block]} selected={block} autoExplain onSelect={vi.fn()} onNavigate={vi.fn()} onReturn={vi.fn()} canReturn={false} onClose={vi.fn()}/>);
  await screen.findByText('The loss pulls matching embeddings together.');
  const paragraphs=view.container.querySelectorAll('.pdf-study-summary p');
  expect(paragraphs).toHaveLength(2);
  expect(paragraphs[0]).toHaveTextContent('The loss pulls matching embeddings together.');
  expect(paragraphs[1]).toHaveTextContent('Optimizing it increases the positive score relative to the negatives.');
 });
 it('keeps a cached one-line summary as one paragraph',async()=>{
  mocks.post.mockReset().mockResolvedValue({data:{data:explanation}});
  const view=render(<PdfFormulaStudy target={{dataset:'d',document:'doc'}} sourceKey="legacy-one-line" blocks={[block]} selected={block} autoExplain onSelect={vi.fn()} onNavigate={vi.fn()} onReturn={vi.fn()} canReturn={false} onClose={vi.fn()}/>);
  await screen.findByText('Summary');
  expect(view.container.querySelectorAll('.pdf-study-summary p')).toHaveLength(1);
 });
 it('shows exact evidence separately from generated interpretation and navigates to it',async()=>{
 mocks.post.mockResolvedValue({data:{data:explanation}});
 const navigate=vi.fn();
 render(<PdfFormulaStudy target={{dataset:'d',document:'doc'}} sourceKey="hash" blocks={[block]} selected={block} autoExplain onSelect={vi.fn()} onNavigate={navigate} onReturn={vi.fn()} canReturn onClose={vi.fn()}/>);
 expect(await screen.findByText('Summary')).toBeInTheDocument();
 expect(screen.getByText('Exact source quote')).toBeInTheDocument();
 fireEvent.click(screen.getAllByRole('button',{name:'Source · p. 2'})[0]);
 expect(navigate).toHaveBeenCalledWith(expect.objectContaining({page:1,text:'Exact source quote'}));
 fireEvent.mouseLeave(screen.getByRole('complementary'));
 expect(screen.getByText('Summary')).toBeInTheDocument();
 });
 it('aborts old explanations on selection changes and ignores their results',async()=>{
 let finish!:(value:unknown)=>void;
 mocks.post.mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve})).mockResolvedValue({data:{data:{...explanation,summary:'New formula'}}});
 const props={target:{dataset:'d',document:'doc'},sourceKey:'hash',blocks:[block],autoExplain:true,onSelect:vi.fn(),onNavigate:vi.fn(),onReturn:vi.fn(),canReturn:false,onClose:vi.fn()};
 const view=render(<PdfFormulaStudy {...props} selected={block}/>);
 const oldSignal=mocks.post.mock.calls[mocks.post.mock.calls.length-1][2].signal;
 view.rerender(<PdfFormulaStudy {...props} selected={{...block,id:'new'}}/>);
 await waitFor(()=>expect(screen.getByText('New formula')).toBeInTheDocument());
 expect(oldSignal.aborted).toBe(true);
 await act(async()=>finish({data:{data:explanation}}));
 expect(screen.queryByText('Summary')).toBeNull();
 });
});

describe('explanation lifecycle',()=>{
 it('shows actionable errors, supports retry/cancel, and resets cache when the source changes',async()=>{
 mocks.post.mockReset().mockRejectedValueOnce({response:{data:{data:{error_message:'Select a model first'}}}}).mockResolvedValueOnce({data:{data:explanation}});
 const props={target:{dataset:'d',document:'doc'},blocks:[block],selected:block,autoExplain:true,onSelect:vi.fn(),onNavigate:vi.fn(),onReturn:vi.fn(),canReturn:false,onClose:vi.fn()};
 const view=render(<PdfFormulaStudy {...props} sourceKey="old-source"/>);
 expect(await screen.findByRole('alert')).toHaveTextContent('Select a model first');
 fireEvent.click(screen.getByRole('button',{name:'Retry explanation'}));
 expect(await screen.findByText('Summary')).toBeInTheDocument();
 expect(mocks.post.mock.calls[1][1]).toEqual({force:false,language:'en'});
 let finish!:(value:unknown)=>void;
 mocks.post.mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve}));
 view.rerender(<PdfFormulaStudy {...props} sourceKey="new-source"/>);
 expect(screen.queryByText('Summary')).toBeNull();
 fireEvent.click(screen.getByRole('button',{name:'Cancel'}));
 expect(screen.getByRole('status')).toHaveTextContent('Cancelled');
 expect(mocks.post.mock.calls[2][2].signal.aborted).toBe(true);
 await act(async()=>finish({data:{data:explanation}}));
 expect(screen.queryByText('Summary')).toBeNull();
 });
 it('catalog selection previews without automatically starting a model request',()=>{
 mocks.post.mockReset();
 const select=vi.fn();
 render(<PdfFormulaStudy target={{dataset:'d',document:'doc'}} sourceKey="hash" blocks={[block]} onSelect={select} onNavigate={vi.fn()} onReturn={vi.fn()} canReturn={false} onClose={vi.fn()}/>);
 fireEvent.change(screen.getByRole('combobox',{name:'Formula catalog'}),{target:{value:'f'}});
 expect(select).toHaveBeenCalledWith(block);
 expect(mocks.post).not.toHaveBeenCalled();
 });
 it('keeps the formula catalog available before an explanation is generated',()=>{
  mocks.post.mockReset();
  const select=vi.fn();
  render(<PdfFormulaStudy target={{dataset:'d',document:'doc'}} sourceKey="catalog" blocks={[block]} selected={block} compact onSelect={select} onNavigate={vi.fn()} onReturn={vi.fn()} canReturn={false} onClose={vi.fn()}/>);
  fireEvent.click(screen.getByText('Formula catalog',{selector:'summary'}));
  expect(screen.getByRole('combobox',{name:'Formula catalog'})).toBeInTheDocument();
  expect(mocks.post).not.toHaveBeenCalled();
 });
});


describe('source evidence selection',()=>{
 it('shows only uniquely cited quotes in server order, retaining plain source math',async()=>{
 const response={...explanation,symbols:[{symbol:'x',meaning:'Input',evidence_ids:['second','first','second'],inferred:false}],evidence:[{id:'first',text:'Original $x$ quote',page:0},{id:'unused',text:'Uncited nearby block',page:0},{id:'second',text:'Second exact quote',page:1}]};
 mocks.post.mockReset().mockResolvedValue({data:{data:response}});
 const view=render(<PdfFormulaStudy target={{dataset:'d',document:'doc'}} sourceKey="citations" blocks={[block]} selected={block} autoExplain onSelect={vi.fn()} onNavigate={vi.fn()} onReturn={vi.fn()} canReturn={false} onClose={vi.fn()}/>);
 await screen.findByText('Original $x$ quote');
 expect(screen.queryByText('Uncited nearby block')).toBeNull();
 expect(Array.from(view.container.querySelectorAll('blockquote')).map(node=>node.textContent)).toEqual(['Original $x$ quote','Second exact quote']);
 expect(view.container.querySelector('blockquote .katex')).toBeNull();
 });
});


it('shows a proposed formula repair, preserves collapsed original LaTeX, and never calls the model on symbol clicks',async()=>{
 mocks.post.mockReset().mockResolvedValue({data:{data:{...explanation,display_latex:String.raw`\frac{x}{y}`,repair_note:'Restored a missing denominator.',symbols:[{symbol:'x',meaning:'Numerator',evidence_ids:['e'],inferred:true},{symbol:'y',meaning:'Denominator',evidence_ids:['e'],inferred:true}]}}});
 const damaged={...block,latex:String.raw`\frac{x}{`};
 const view=render(<PdfFormulaStudy target={{dataset:'d',document:'doc'}} sourceKey="repair" blocks={[damaged]} selected={damaged} autoExplain onSelect={vi.fn()} onNavigate={vi.fn()} onReturn={vi.fn()} canReturn={false} onClose={vi.fn()}/>);
 expect(await screen.findByText('Summary')).toBeInTheDocument();
 expect(screen.getByText('Restored a missing denominator.')).toBeInTheDocument();
 const raw=view.container.querySelector('.pdf-symbol-raw') as HTMLDetailsElement;
 expect(raw.open).toBe(false);
 fireEvent.click(screen.getByRole('button',{name:'Symbol: x'}));
 expect(screen.getAllByText('Numerator')).toHaveLength(2);
 fireEvent.click(screen.getByRole('button',{name:'Symbol: y'}));
 expect(screen.getAllByText('Denominator')).toHaveLength(2);
 expect(mocks.post).toHaveBeenCalledTimes(1);
 fireEvent.click(raw.querySelector('summary')!);
 expect(raw.textContent).toContain(damaged.latex);
});
it('falls back to the extracted formula when a proposed repair cannot render',async()=>{
 mocks.post.mockReset().mockResolvedValue({data:{data:{...explanation,display_latex:'x^',repair_note:'Bad repair'}}});
 const view=render(<PdfFormulaStudy target={{dataset:'d',document:'doc'}} sourceKey="invalid-repair" blocks={[block]} selected={block} autoExplain onSelect={vi.fn()} onNavigate={vi.fn()} onReturn={vi.fn()} canReturn={false} onClose={vi.fn()}/>);
 await screen.findByText('Summary');
 expect(screen.queryByText('Bad repair')).toBeNull();
 expect(view.container.querySelector('.pdf-interactive-equation .katex-html')?.textContent).toContain('x2');
 expect(mocks.post).toHaveBeenCalledTimes(1);
});

it('retains the previous explanation through regeneration failure and cancellation',async()=>{
 mocks.post.mockResolvedValueOnce({data:{data:explanation}});
 const {result}=renderHook(()=>useFormulaExplanation({dataset:'d',document:'doc'},'retained','f',true));
 await waitFor(()=>expect(result.current.status).toBe('ready'));
 let rejectUpdate:(reason:unknown)=>void=()=>{};
 mocks.post.mockImplementationOnce(()=>new Promise((_,reject)=>{rejectUpdate=reject;}));
 act(()=>{void result.current.load(true);});
 expect(result.current.status).toBe('loading');
 expect(result.current.data?.summary).toBe('Summary');
 await act(async()=>{rejectUpdate(new Error('offline'));});
 expect(result.current.status).toBe('error');
 expect(result.current.data?.summary).toBe('Summary');
 mocks.post.mockImplementationOnce(()=>new Promise(()=>{}));
 act(()=>{void result.current.load(true);});
 act(()=>result.current.cancel());
 expect(result.current.status).toBe('cancelled');
 expect(result.current.data?.summary).toBe('Summary');
});

it('isolates late responses when two papers share a source hash and formula ID',async()=>{
 let finishOld!:(value:unknown)=>void;
 mocks.post.mockReset().mockImplementationOnce(()=>new Promise(resolve=>{finishOld=resolve;})).mockResolvedValueOnce({data:{data:{...explanation,summary:'Second paper'}}});
 const view=renderHook(({document})=>useFormulaExplanation({dataset:'d',document},'shared-hash','f',true),{initialProps:{document:'first-paper'}});
 const oldSignal=mocks.post.mock.calls[0][2].signal;
 view.rerender({document:'second-paper'});
 await waitFor(()=>expect(view.result.current.data?.summary).toBe('Second paper'));
 expect(oldSignal.aborted).toBe(true);
 await act(async()=>finishOld({data:{data:{...explanation,summary:'Late first paper'}}}));
 expect(view.result.current.data?.summary).toBe('Second paper');
 view.unmount();
 const oldPaper=renderHook(()=>useFormulaExplanation({dataset:'d',document:'first-paper'},'shared-hash','f',false));
 expect(oldPaper.result.current.data).toBeUndefined();
 expect(mocks.post).toHaveBeenCalledTimes(2);
});

it('does not replace a newer successful update with a cancelled late response',async()=>{
 let finishCancelled!:(value:unknown)=>void;
 mocks.post.mockReset().mockResolvedValueOnce({data:{data:explanation}});
 const view=renderHook(()=>useFormulaExplanation({dataset:'d',document:'doc'},'cancel-update-race','f',true));
 await waitFor(()=>expect(view.result.current.status).toBe('ready'));
 mocks.post.mockImplementationOnce(()=>new Promise(resolve=>{finishCancelled=resolve;}));
 act(()=>{void view.result.current.load(true);});
 act(()=>view.result.current.cancel());
 expect(view.result.current.data?.summary).toBe('Summary');
 mocks.post.mockResolvedValueOnce({data:{data:{...explanation,summary:'Fresh update'}}});
 await act(async()=>{await view.result.current.load(true);});
 await act(async()=>finishCancelled({data:{data:{...explanation,summary:'Cancelled update'}}}));
 expect(view.result.current.data?.summary).toBe('Fresh update');
 view.unmount();
 const reopened=renderHook(()=>useFormulaExplanation({dataset:'d',document:'doc'},'cancel-update-race','f',true));
 await waitFor(()=>expect(reopened.result.current.data?.summary).toBe('Fresh update'));
 expect(mocks.post).toHaveBeenCalledTimes(3);
});

it('cancels only the closing viewer while another viewer awaits the same formula',async()=>{
 const resolveRequests:Array<(value:unknown)=>void>=[];
 mocks.post.mockReset().mockImplementation(()=>new Promise(resolve=>resolveRequests.push(resolve)));
 const first=renderHook(()=>useFormulaExplanation({dataset:'d',document:'doc'},'concurrent-viewers','f',true));
 const second=renderHook(()=>useFormulaExplanation({dataset:'d',document:'doc'},'concurrent-viewers','f',true));
 first.unmount();
 expect(mocks.post.mock.calls[0][2].signal.aborted).toBe(true);
 expect(mocks.post.mock.calls[1][2].signal.aborted).toBe(false);
 await act(async()=>resolveRequests[1]({data:{data:explanation}}));
 expect(second.result.current.data?.summary).toBe('Summary');
 await act(async()=>resolveRequests[0]({data:{data:{...explanation,summary:'Closed viewer'}}}));
 expect(second.result.current.data?.summary).toBe('Summary');
});


it('invalidates a mounted formula and aborts its request on session change',async()=>{
 const identity=vi.spyOn(AgentAppsAuth,'getSessionIdentity').mockReturnValue('old-session');
 let finish!:(value:unknown)=>void;
 mocks.post.mockReset().mockResolvedValueOnce({data:{data:explanation}}).mockImplementation(()=>new Promise(resolve=>{finish=resolve}));
 const hook=renderHook(()=>useFormulaExplanation({dataset:'d',document:'doc'},'session-invalidation','f',false));
 await act(async()=>{await hook.result.current.load();});
 expect(hook.result.current.data?.summary).toBe('Summary');
 act(()=>{void hook.result.current.load(true);});
 const signal=mocks.post.mock.calls[1][2].signal;
 identity.mockReturnValue('new-session');
 act(()=>window.dispatchEvent(new Event(AUTH_USER_CHANGE_EVENT)));
 expect(signal.aborted).toBe(true);
 expect(hook.result.current.data).toBeUndefined();
 await act(async()=>finish({data:{data:explanation}}));
 expect(hook.result.current.data).toBeUndefined();
 hook.unmount();identity.mockRestore();
});
