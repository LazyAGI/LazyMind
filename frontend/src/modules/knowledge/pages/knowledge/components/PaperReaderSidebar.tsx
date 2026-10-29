import {useId,type ReactNode} from 'react';
import {useTranslation} from 'react-i18next';
import type {PdfIRBlock,PdfIRDocument} from '@/modules/knowledge/api/pdfIR';
export function paperOutline(document?:PdfIRDocument):PdfIRBlock[]{
 if(!document)return [];
 return document.blocks.filter(block=>{
  const text=block.text?.trim();if(!text||!block.id||block.page<0||text.length>160||text.includes('\n'))return false;
  if(block.type==='heading'||block.type==='title')return true;
  return block.type==='text'&&(/^\d+(?:\.\d+){0,3}\.?\s+\S/.test(text)||/^(abstract|references|conclusions?|acknowledg(?:e)?ments?|appendix(?:\s+[A-Z])?|摘要|参考文献|结论|致谢)$/i.test(text));
 });
}
interface Props {isPdf?:boolean;document?:PdfIRDocument;activeTab:string;onTabChange:(tab:string)=>void;onNavigate:(id:string)=>void;onCollapse:()=>void;references:ReactNode;vocabulary?:ReactNode;segments?:ReactNode;referenceCount?:number}
export function PaperReaderSidebar({isPdf=true,document,activeTab,onTabChange,onNavigate,onCollapse,references,vocabulary,segments,referenceCount}:Props){
 const {t}=useTranslation(),id=useId();const outline=paperOutline(document);
 const vocabularyContent=vocabulary||(isPdf?<p className="paper-shell-empty">{t('paperShell.vocabularyUnavailable',{defaultValue:'Vocabulary is not enabled in this workspace. Contact your administrator to enable it.'})}</p>:undefined);
 const tabs=[{key:'outline',label:t('paperShell.outline',{defaultValue:'Outline'})},{key:'references',label:t('paperShell.references',{defaultValue:'References'})},...(vocabularyContent?[{key:'vocabulary',label:t('paperShell.vocabulary',{defaultValue:'Vocabulary'})}]:[])];
 return <aside className="paper-reader-sidebar" aria-label={t('paperShell.navigation',{defaultValue:'Paper navigation'})}>
  <header><span className="paper-shell-eyebrow">{t('paperShell.library',{defaultValue:'READING DESK'})}</span><button type="button" onClick={onCollapse} aria-label={t('paperShell.collapseNavigation',{defaultValue:'Collapse paper navigation'})}>‹</button></header>
  <div className="paper-reader-sidebar__tabs" role="tablist">{tabs.map(tab=><button type="button" key={tab.key} role="tab" id={`${id}-${tab.key}`} aria-controls={`${id}-${tab.key}-panel`} aria-selected={activeTab===tab.key} onClick={()=>onTabChange(tab.key)}>{tab.label}{tab.key==='references'&&!!referenceCount&&<span>{referenceCount}</span>}</button>)}</div>
  <div role="tabpanel" id={`${id}-outline-panel`} aria-labelledby={`${id}-outline`} hidden={activeTab!=='outline'} className="paper-reader-sidebar__pane">
   {outline.length?<nav aria-label={t('paperShell.outline',{defaultValue:'Outline'})}><ol className="paper-outline">{outline.map(block=><li key={block.id}><button type="button" onClick={()=>onNavigate(block.id)} style={{paddingLeft:/^\d+\.\d+/.test(block.text||'')?'20px':undefined}}><span>{block.text}</span><small>{block.page+1}</small></button></li>)}</ol></nav>:<p className="paper-shell-empty">{!isPdf?t('paperShell.noDocumentOutline',{defaultValue:'An outline is not available for this document.'}):document?t('paperShell.noOutline',{defaultValue:'No section headings were identified in this PDF.'}):t('paperShell.parseOutline',{defaultValue:'Parse the original PDF to see its outline.'})}</p>}
   {segments&&<details className="paper-sidebar-segments"><summary>{t('paperShell.segments',{defaultValue:'Document chunks'})}</summary>{segments}</details>}
  </div>
  <div role="tabpanel" id={`${id}-references-panel`} aria-labelledby={`${id}-references`} hidden={activeTab!=='references'} className="paper-reader-sidebar__pane">{references}</div>
  {vocabularyContent&&<div role="tabpanel" id={`${id}-vocabulary-panel`} aria-labelledby={`${id}-vocabulary`} hidden={activeTab!=='vocabulary'} className="paper-reader-sidebar__pane">{vocabularyContent}</div>}
 </aside>;
}
