import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';
import {beforeAll, describe, expect, it, vi} from 'vitest';
import i18n from '@/i18n';
import {PdfFormulaStudy} from './PdfFormulaStudy';
const mocks=vi.hoisted(()=>({post:vi.fn()}));
vi.mock('@/components/request',()=>({axiosInstance:mocks,BASE_URL:''}));
const block={id:'f',type:'formula',page:2,latex:'x^2'};
const explanation={summary:'Summary',symbols:[{symbol:'x',meaning:'Input',evidence_ids:['e'],inferred:false}],steps:[],evidence:[{id:'e',text:'Exact source quote',page:1,bbox:[0,0,1,.1]}],warnings:[]};
beforeAll(async()=>{await i18n.changeLanguage('en-US');});
describe('formula study panel',()=>{
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


it('requests the exact clicked source occurrence as a separate focused explanation',async()=>{
 mocks.post.mockReset().mockResolvedValue({data:{data:{...explanation,symbols:[{symbol:'x^2',meaning:'Squared variable',evidence_ids:['e'],inferred:true}]}}});
 render(<PdfFormulaStudy target={{dataset:'d',document:'doc'}} sourceKey="focus-api" blocks={[block]} selected={block} onSelect={vi.fn()} onNavigate={vi.fn()} onReturn={vi.fn()} canReturn={false} onClose={vi.fn()}/>);
 fireEvent.click(screen.getByRole('button',{name:'Symbol: x^2'}));
 expect(await screen.findByText('Squared variable')).toBeInTheDocument();
 expect(mocks.post).toHaveBeenCalledTimes(1);
 expect(mocks.post.mock.calls[0][0]).toContain('/formulas/f/explanation');
 expect(mocks.post.mock.calls[0][1]).toEqual({force:false,language:'en',focus_symbol:'x^2'});
 expect(mocks.post.mock.calls[0][2].signal).toBeInstanceOf(AbortSignal);
});
