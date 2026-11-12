import {useRef,useState, type ReactNode} from 'react';
import {ArrowUpOutlined, BookOutlined, CloseOutlined, EditOutlined, QuestionCircleOutlined, TranslationOutlined} from '@ant-design/icons';
import type {PdfTextSelection} from './RenderPdf';
import {PDF_NOTE_COLORS,type PdfNoteColor} from './pdfNoteHighlights';
import {v4 as uuidv4} from 'uuid';
import './PdfSelectionComposer.css';

export interface PdfComposerAction {key:string;label:string;disabled?:boolean;title?:string;onClick:()=>void}
interface Props {
 selection:PdfTextSelection;
 onAsk?:(selection:PdfTextSelection)=>void;
 onNote?:(selection:PdfTextSelection,note:string,color:PdfNoteColor,id:string)=>Promise<void>;
 initialMode?:'ask'|'note';
 onModeChange?:(mode:'ask'|'note')=>void;
 onHighlight?:(color:PdfNoteColor)=>void;
 onTranslate?:()=>void;
 translationDisabled?:boolean;
 translationTitle?:string;
 translationHelp?:ReactNode;
 actions?:PdfComposerAction[];
 onClose:()=>void;
}
/** Editing uses the captured PDF selection; textarea selection never replaces it. */
export function PdfSelectionComposer({selection,onAsk,onNote,initialMode,onModeChange,onHighlight,onTranslate,translationDisabled,translationTitle,translationHelp,actions=[],onClose}:Props){
 const [mode,setMode]=useState<'ask'|'note'>(()=>initialMode==='note'&&onNote?'note':onAsk?'ask':'note');
 const chooseMode=(next:'ask'|'note')=>{setMode(next);onModeChange?.(next);};
 const [draft,setDraft]=useState('');
 const [noteColor,setNoteColor]=useState<PdfNoteColor>('yellow');
 const [saving,setSaving]=useState(false);
 const [saveError,setSaveError]=useState('');
 const noteId=useRef(uuidv4());
 const composing=draft.trim().length>0;
 const primaryActions=actions.filter(action=>action.key==='explanation'||action.key==='vocabulary');
 const submit=async()=>{
  if(saving)return;
  if(mode==='ask'&&onAsk){onAsk({...selection,...(draft.trim()?{question:draft.trim()}:{})});onClose();return;}
  if(mode!=='note'||!onNote)return;
  if(!draft.trim()){onHighlight?.(noteColor);onClose();return;}
  setSaveError('');setSaving(true);
  try{await onNote(selection,draft.trim(),noteColor,noteId.current);onClose();}
  catch(error){setSaveError(error instanceof Error?error.message:'笔记保存失败，请重试。');}
  finally{setSaving(false);}
 };
 return <div className={`pdf-selection-composer${composing?' is-composing':''}${mode==='note'?' is-note-mode':''}`} role="dialog" aria-label="选中文本操作" aria-busy={saving} onKeyDown={event=>{if(event.key==='Escape'&&!saving){event.stopPropagation();onClose();}if(event.key==='Enter'&&(event.metaKey||event.ctrlKey)){event.preventDefault();void submit();}}}>
  <button className="pdf-selection-close" type="button" aria-label="关闭" data-tooltip="关闭" disabled={saving} onClick={onClose}><CloseOutlined/></button>
  <textarea aria-label="向 AI 提问或记笔记" placeholder={mode==='note'?'记下这段文字带给你的想法…':'向 AI 提问或记笔记'} value={draft} disabled={saving} onChange={event=>{if(saveError&&event.target.value!==draft){noteId.current=uuidv4();setSaveError('');}setDraft(event.target.value);}} rows={2}/>
  {saveError&&<p className="pdf-selection-save-error" role="alert">{saveError}</p>}
  <div className="pdf-selection-composer-footer">
   <div className="pdf-selection-modes" aria-label="操作类型">
    {onAsk&&<button type="button" aria-pressed={mode==='ask'} disabled={saving} onClick={()=>chooseMode('ask')}>问 AI</button>}
    {onNote&&<button type="button" aria-pressed={mode==='note'} disabled={saving} onClick={()=>chooseMode('note')}>笔记</button>}
   </div>
   <div className="pdf-selection-icons">
    {mode==='note'&&onNote&&<div className="pdf-selection-colors" role="group" aria-label="高亮颜色">{PDF_NOTE_COLORS.map(color=><button type="button" key={color.key} className="pdf-selection-color" style={{background:color.swatch}} aria-label={color.label} data-tooltip={color.label} aria-pressed={noteColor===color.key} disabled={saving} onClick={()=>{if(saveError&&color.key!==noteColor){noteId.current=uuidv4();setSaveError('');}setNoteColor(color.key);}}/>)}</div>}
    {mode==='note'&&!composing&&onNote&&<button type="button" className="pdf-selection-mark" aria-label="高亮并记笔记" data-tooltip="高亮并记笔记" disabled={saving} onClick={()=>void submit()}><EditOutlined/></button>}
    {(mode==='ask'||!onAsk)&&onTranslate&&<span className="pdf-selection-translation" data-tooltip={translationDisabled&&translationTitle?`翻译 · ${translationTitle}`:translationTitle||'翻译'}><button type="button" className="pdf-selection-icon" aria-label="翻译" disabled={translationDisabled} onClick={()=>{onTranslate();onClose();}}><TranslationOutlined/></button></span>}
    {(mode==='ask'||!onAsk)&&!composing&&primaryActions.map(action=><button type="button" className="pdf-selection-primary-action" key={action.key} aria-label={action.label} disabled={action.disabled} data-tooltip={`${action.key==='explanation'?'快速解释选中文字':action.label}${action.title?` · ${action.title}`:''}`} onClick={()=>{action.onClick();onClose();}}>{action.key==='explanation'?<QuestionCircleOutlined/>:<BookOutlined/>}</button>)}
    {composing&&(onAsk||onNote)&&<button type="button" className="pdf-selection-send" aria-label={mode==='ask'?'向 AI 提问':'保存笔记'} data-tooltip={mode==='ask'?'向 AI 提问':'保存笔记'} disabled={saving} onClick={()=>void submit()}>{saving?'…':<ArrowUpOutlined/>}</button>}
   </div>
  </div>
  {translationDisabled&&translationHelp&&<div className="pdf-selection-help">{translationHelp}</div>}
 </div>;
}
