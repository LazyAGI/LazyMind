import {useState, type ReactNode} from 'react';
import {ArrowUpOutlined, BulbOutlined, CloseOutlined, TranslationOutlined} from '@ant-design/icons';
import type {PdfTextSelection} from './RenderPdf';
import './PdfSelectionComposer.css';

export interface PdfComposerAction {key:string;label:string;disabled?:boolean;title?:string;onClick:()=>void}
interface Props {
 selection:PdfTextSelection;
 onAsk?:(selection:PdfTextSelection)=>void;
 onNote?:(selection:PdfTextSelection,note:string)=>void;
 onTranslate?:()=>void;
 translationDisabled?:boolean;
 translationTitle?:string;
 translationHelp?:ReactNode;
 onExplain?:()=>void;
 actions?:PdfComposerAction[];
 onClose:()=>void;
}
/** Editing uses the captured PDF selection; textarea selection never replaces it. */
export function PdfSelectionComposer({selection,onAsk,onNote,onTranslate,translationDisabled,translationTitle,translationHelp,onExplain,actions=[],onClose}:Props){
 const [mode,setMode]=useState<'ask'|'note'>(onAsk?'ask':'note');
 const [draft,setDraft]=useState('');
 const submit=()=>{
  if(mode==='ask'&&onAsk)onAsk({...selection,...(draft.trim()?{question:draft.trim()}:{})});
  else if(mode==='note'&&onNote)onNote(selection,draft.trim());
  else return;
  onClose();
 };
 return <div className="pdf-selection-composer" role="dialog" aria-label="选中文本操作" onKeyDown={event=>{if(event.key==='Escape'){event.stopPropagation();onClose();}if(event.key==='Enter'&&(event.metaKey||event.ctrlKey)){event.preventDefault();submit();}}}>
  <button className="pdf-selection-close" type="button" aria-label="关闭" title="关闭" onClick={onClose}><CloseOutlined/></button>
  <textarea aria-label="向 AI 提问或记笔记" placeholder="向 AI 提问或记笔记" value={draft} onChange={event=>setDraft(event.target.value)} rows={2}/>
  <div className="pdf-selection-composer-footer">
   <div className="pdf-selection-modes" aria-label="操作类型">
    {onAsk&&<button type="button" aria-pressed={mode==='ask'} onClick={()=>setMode('ask')}>问 AI</button>}
    {onNote&&<button type="button" aria-pressed={mode==='note'} onClick={()=>setMode('note')}>笔记</button>}
   </div>
   <div className="pdf-selection-icons">
    {onTranslate&&<span title={translationTitle||'翻译'}><button type="button" className="pdf-selection-icon" aria-label="翻译" disabled={translationDisabled} onClick={()=>{onTranslate();onClose();}}><TranslationOutlined/></button></span>}
    {onExplain&&<button type="button" className="pdf-selection-icon" aria-label="解释选中文本" title="解释选中文本" onClick={()=>{onExplain();onClose();}}><BulbOutlined/></button>}
    {actions.length>0&&<details className="pdf-selection-more"><summary aria-label="更多操作" title="更多操作"><span className="pdf-selection-sr-only">更多操作</span>⋯</summary><div>{actions.map(action=><button type="button" key={action.key} disabled={action.disabled} title={action.title} onClick={()=>{action.onClick();onClose();}}>{action.label}</button>)}</div></details>}
    {(onAsk||onNote)&&<button type="button" className="pdf-selection-send" aria-label={mode==='ask'?'向 AI 提问':'添加笔记草稿'} title={mode==='ask'?'向 AI 提问':'添加笔记草稿'} onClick={submit}><ArrowUpOutlined/></button>}
   </div>
  </div>
  {translationDisabled&&translationHelp&&<div className="pdf-selection-help">{translationHelp}</div>}
 </div>;
}
