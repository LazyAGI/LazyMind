import {useState, type ReactNode} from 'react';
import {ArrowUpOutlined, BookOutlined, CloseOutlined, EditOutlined, QuestionCircleOutlined, TranslationOutlined} from '@ant-design/icons';
import type {PdfTextSelection} from './RenderPdf';
import {PDF_NOTE_COLORS,type PdfNoteColor} from './pdfNoteHighlights';
import './PdfSelectionComposer.css';

export interface PdfComposerAction {key:string;label:string;disabled?:boolean;title?:string;onClick:()=>void}
interface Props {
 selection:PdfTextSelection;
 onAsk?:(selection:PdfTextSelection)=>void;
 onNote?:(selection:PdfTextSelection,note:string)=>void;
 onHighlight?:(color:PdfNoteColor)=>void;
 onTranslate?:()=>void;
 translationDisabled?:boolean;
 translationTitle?:string;
 translationHelp?:ReactNode;
 actions?:PdfComposerAction[];
 onClose:()=>void;
}
/** Editing uses the captured PDF selection; textarea selection never replaces it. */
export function PdfSelectionComposer({selection,onAsk,onNote,onHighlight,onTranslate,translationDisabled,translationTitle,translationHelp,actions=[],onClose}:Props){
 const [mode,setMode]=useState<'ask'|'note'>(onAsk?'ask':'note');
 const [draft,setDraft]=useState('');
 const [noteColor,setNoteColor]=useState<PdfNoteColor>('yellow');
 const composing=draft.trim().length>0;
 const primaryActions=actions.filter(action=>action.key==='explanation'||action.key==='vocabulary');
 const moreActions=actions.filter(action=>!primaryActions.includes(action));
 const submit=()=>{
  if(mode==='ask'&&onAsk)onAsk({...selection,...(draft.trim()?{question:draft.trim()}:{})});
  else if(mode==='note'&&onNote){onHighlight?.(noteColor);onNote(selection,draft.trim());}
  else return;
  onClose();
 };
 return <div className={`pdf-selection-composer${composing?' is-composing':''}${mode==='note'?' is-note-mode':''}`} role="dialog" aria-label="选中文本操作" onKeyDown={event=>{if(event.key==='Escape'){event.stopPropagation();onClose();}if(event.key==='Enter'&&(event.metaKey||event.ctrlKey)){event.preventDefault();submit();}}}>
  <button className="pdf-selection-close" type="button" aria-label="关闭" data-tooltip="关闭" onClick={onClose}><CloseOutlined/></button>
  <textarea aria-label="向 AI 提问或记笔记" placeholder="向 AI 提问或记笔记" value={draft} onChange={event=>setDraft(event.target.value)} rows={2}/>
  <div className="pdf-selection-composer-footer">
   <div className="pdf-selection-modes" aria-label="操作类型">
    {onAsk&&<button type="button" aria-pressed={mode==='ask'} onClick={()=>setMode('ask')}>问 AI</button>}
    {onNote&&<button type="button" aria-pressed={mode==='note'} onClick={()=>setMode('note')}>笔记</button>}
   </div>
   <div className="pdf-selection-icons">
    {mode==='note'&&!composing&&onNote&&<div className="pdf-selection-colors" role="group" aria-label="高亮颜色">{PDF_NOTE_COLORS.map(color=><button type="button" key={color.key} className="pdf-selection-color" style={{background:color.swatch}} aria-label={color.label} data-tooltip={color.label} aria-pressed={noteColor===color.key} onClick={()=>setNoteColor(color.key)}/>)}</div>}
    {mode==='note'&&!composing&&onNote&&<button type="button" className="pdf-selection-mark" aria-label="高亮并记笔记" data-tooltip="高亮并记笔记" onClick={submit}><EditOutlined/></button>}
    {(mode==='ask'||!onAsk)&&onTranslate&&<span className="pdf-selection-translation" data-tooltip={translationDisabled&&translationTitle?`翻译 · ${translationTitle}`:translationTitle||'翻译'}><button type="button" className="pdf-selection-icon" aria-label="翻译" disabled={translationDisabled} onClick={()=>{onTranslate();onClose();}}><TranslationOutlined/></button></span>}
    {(mode==='ask'||!onAsk)&&!composing&&primaryActions.map(action=><button type="button" className="pdf-selection-primary-action" key={action.key} aria-label={action.label} disabled={action.disabled} data-tooltip={`${action.key==='explanation'?'快速解释选中文字':action.label}${action.title?` · ${action.title}`:''}`} onClick={()=>{action.onClick();onClose();}}>{action.key==='explanation'?<QuestionCircleOutlined/>:<BookOutlined/>}</button>)}
    {(mode==='ask'||!onAsk)&&moreActions.length>0&&<details className="pdf-selection-more"><summary aria-label="更多操作" data-tooltip="更多操作"><span className="pdf-selection-sr-only">更多操作</span>⋯</summary><div>{moreActions.map(action=><button type="button" key={action.key} disabled={action.disabled} title={action.title} onClick={()=>{action.onClick();onClose();}}>{action.label}</button>)}</div></details>}
    {composing&&(onAsk||onNote)&&<button type="button" className="pdf-selection-send" aria-label={mode==='ask'?'向 AI 提问':'添加笔记草稿'} data-tooltip={mode==='ask'?'向 AI 提问':'添加笔记草稿'} onClick={submit}><ArrowUpOutlined/></button>}
   </div>
  </div>
  {translationDisabled&&translationHelp&&<div className="pdf-selection-help">{translationHelp}</div>}
 </div>;
}
