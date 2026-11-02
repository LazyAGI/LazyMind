import {useId,type ReactNode} from 'react';
import {usePaperReaderLabels,paperVisualLabel} from '@/components/ui/paperReaderLabels';
import {useTranslation} from 'react-i18next';
import type {PdfIRBlock,PdfIRDocument} from '@/modules/knowledge/api/pdfIR';
import type {PdfNativeOutlineItem} from '@/modules/knowledge/api/pdfOutline';
export function paperOutline(document?:PdfIRDocument):PdfIRBlock[]{
 if(!document)return [];
 return document.blocks.filter(block=>{
  const text=block.text?.trim();if(!text||!block.id||block.page<0||text.length>160||text.includes('\n'))return false;
  if(block.type==='heading'||block.type==='title')return true;
  return block.type==='text'&&(/^\d+(?:\.\d+){0,3}\.?\s+\S/.test(text)||/^(abstract|references|conclusions?|acknowledg(?:e)?ments?|appendix(?:\s+[A-Z])?|摘要|参考文献|结论|致谢)$/i.test(text));
 });
}
interface Props {paperMode?:boolean;currentPage?:number;isPdf?:boolean;document?:PdfIRDocument;nativeOutline?:PdfNativeOutlineItem[];activeTab:string;onTabChange:(tab:string)=>void;onNavigate:(id:string)=>void;onNavigatePage?:(page:number)=>void;onCollapse:()=>void;references:ReactNode;vocabulary?:ReactNode;segments?:ReactNode;referenceCount?:number}
export function PaperReaderSidebar({paperMode=false,currentPage=1,isPdf=true,document,nativeOutline=[],activeTab,onTabChange,onNavigate,onNavigatePage,onCollapse,references,vocabulary,segments,referenceCount}:Props){
 const {t}=useTranslation(),id=useId();const outline=nativeOutline.length?nativeOutline.map(item=>({id:item.id,text:item.title,page:item.page-1,depth:item.depth,native:true})):paperOutline(document).map(block=>({...block,depth:/^\d+\.\d+/.test(block.text||'')?1:0,native:false}));
 const {label}=usePaperReaderLabels();
 const visuals=paperMode?document?.blocks.filter(block=>['image','table','formula'].includes(block.type))||[]:[];
 const precedingSections=outline.filter(block=>block.page<currentPage);
 const currentSection=precedingSections[precedingSections.length-1]?.id;
 const vocabularyContent=vocabulary||(paperMode&&isPdf?<p className="paper-shell-empty">{t('paperShell.vocabularyUnavailable',{defaultValue:'Vocabulary is not enabled in this workspace. Contact your administrator to enable it.'})}</p>:undefined);
 const tabs=[...(paperMode||outline.length?[{key:'outline',label:t('paperShell.outline',{defaultValue:'Outline'})}]:[]),...(!paperMode&&segments?[{key:'segments',label:t('paperShell.segments',{defaultValue:'Document chunks'})}]:[]),{key:'references',label:t('paperShell.references',{defaultValue:'References'})},...(visuals.length?[{key:'visuals',label:label('visuals')}]:[]),...(vocabularyContent?[{key:'vocabulary',label:t('paperShell.vocabulary',{defaultValue:'Vocabulary'})}]:[])];
 const visibleTab=tabs.some(tab=>tab.key===activeTab)?activeTab:tabs[0].key;
 return <aside className="paper-reader-sidebar" aria-label={paperMode?t('paperShell.navigation',{defaultValue:'Paper navigation'}):t('paperShell.documentNavigation',{defaultValue:'Document navigation'})}>
  <header><span className="paper-shell-eyebrow">{paperMode?t('paperShell.library',{defaultValue:'READING DESK'}):t('paperShell.document',{defaultValue:'Document'})}</span><button type="button" onClick={onCollapse} aria-label={paperMode?t('paperShell.collapseNavigation',{defaultValue:'Collapse paper navigation'}):t('paperShell.collapseDocumentNavigation',{defaultValue:'Collapse document navigation'})}>‹</button></header>
  <div className="paper-reader-sidebar__tabs" role="tablist">{tabs.map(tab=><button type="button" key={tab.key} role="tab" id={`${id}-${tab.key}`} aria-controls={`${id}-${tab.key}-panel`} aria-selected={visibleTab===tab.key} onClick={()=>onTabChange(tab.key)}>{tab.label}{tab.key==='references'&&!!referenceCount&&<span>{referenceCount}</span>}</button>)}</div>
  <div role="tabpanel" id={`${id}-outline-panel`} aria-labelledby={`${id}-outline`} hidden={visibleTab!=='outline'} className="paper-reader-sidebar__pane">
   {outline.length?<nav aria-label={t('paperShell.outline',{defaultValue:'Outline'})}><ol className="paper-outline">{outline.map(block=><li key={block.id}><button type="button" aria-current={block.id===currentSection?"location":undefined} onClick={()=>block.native?onNavigatePage?.(block.page+1):onNavigate(block.id)} style={{paddingLeft:block.depth?'20px':undefined}}><span>{block.text}</span><small>{block.page+1}</small></button></li>)}</ol></nav>:<p className="paper-shell-empty">{!isPdf?t('paperShell.noDocumentOutline',{defaultValue:'An outline is not available for this document.'}):t('paperShell.noOutline',{defaultValue:'This PDF has no available outline.'})}</p>}
   {paperMode&&segments&&<details className="paper-sidebar-segments"><summary>{t('paperShell.segments',{defaultValue:'Document chunks'})}</summary>{segments}</details>}
  </div>
  {!paperMode&&segments&&<div role="tabpanel" id={`${id}-segments-panel`} aria-labelledby={`${id}-segments`} hidden={visibleTab!=='segments'} className="paper-reader-sidebar__pane">{segments}</div>}
  <div role="tabpanel" id={`${id}-references-panel`} aria-labelledby={`${id}-references`} hidden={visibleTab!=='references'} className="paper-reader-sidebar__pane">{references}</div>
  {!!visuals.length&&<div role="tabpanel" id={`${id}-visuals-panel`} aria-labelledby={`${id}-visuals`} hidden={visibleTab!=='visuals'} className="paper-reader-sidebar__pane"><ol className="paper-outline">{visuals.map(block=><li key={block.id}><button type="button" onClick={()=>onNavigate(block.id)}><span>{paperVisualLabel(block,document!.blocks,label)}</span><small>{block.page+1}</small></button></li>)}</ol></div>}
  {vocabularyContent&&<div role="tabpanel" id={`${id}-vocabulary-panel`} aria-labelledby={`${id}-vocabulary`} hidden={visibleTab!=='vocabulary'} className="paper-reader-sidebar__pane">{vocabularyContent}</div>}
 </aside>;
}
