import {useEffect,useId,useMemo,useRef,useState} from 'react';
import {useTranslation} from 'react-i18next';
import katex from 'katex';
import type {FormulaEvidence,FormulaExplanation} from '@/modules/knowledge/api/formulaExplanation';
import {FormulaMathText} from './FormulaMathText';
import {interactiveFormulaSymbols} from './interactiveFormulaSymbols';
import {formulaBuiltinMeaning} from './formulaSymbolGlossary';
interface Props {latex:string;originalLatex?:string;sourceKey:string;definitions:FormulaExplanation['symbols'];evidence:FormulaEvidence[];compactEvidence?:boolean;onNavigate:(evidence:FormulaEvidence)=>void}
export function InteractiveFormula({latex,originalLatex,sourceKey,definitions,evidence,compactEvidence=false,onNavigate}:Props){
 const {t,i18n}=useTranslation();
 const zh=(i18n.resolvedLanguage || i18n.language).startsWith('zh');
 const label=(key:string,en:string,cn:string)=>t(`pdfFormula.${key}`,{defaultValue:zh?cn:en});
 const id=useId().replace(/[^a-zA-Z0-9]/g,'');
 const root=useRef<HTMLDivElement>(null);
 const card=useRef<HTMLDivElement>(null);
 const key=`${sourceKey}:${latex}:${JSON.stringify(definitions)}`;
 const [selection,setSelection]=useState<{key:string;index:number;child?:number}>();
 const [copyStatus,setCopyStatus]=useState('');
 const model=useMemo(()=>interactiveFormulaSymbols(latex,definitions.map(definition=>definition.symbol),true),[latex,definitions]);
 const rendered=useMemo(()=>{
  const classes=new Set(model.occurrences.map((_,index)=>`pdfsym-${id}-${index}`));
  let annotated=latex;
  if(model.safe)for(let index=model.occurrences.length-1;index>=0;index--){
   const occurrence=model.occurrences[index];
   annotated=annotated.slice(0,occurrence.start)+`\\htmlClass{pdfsym-${id}-${index}}{${occurrence.latex}}`+annotated.slice(occurrence.end);
  }
  try{return katex.renderToString(annotated,{displayMode:true,throwOnError:true,strict:'ignore',maxExpand:1000,trust:context=>model.safe&&context.command==='\\htmlClass'&&classes.has(context.class)});}
  catch{return undefined;}
 },[latex,model,id]);
 const active=selection?.key===key?selection.index:undefined;
 const parent=active===undefined?undefined:model.occurrences[active];
 const occurrence=selection?.child===undefined?parent:parent?.children?.[selection.child];
 const candidates=(occurrence?.definitionIndices || []).map(index=>definitions[index]);
 const exact=candidates.filter(item=>item.symbol.trim()===occurrence?.latex.trim());
 const relevant=exact.length?exact:candidates;
 const meanings=Array.from(new Set(relevant.map(item=>item.meaning.trim()).filter(Boolean)));
 const definition=relevant.length?{...relevant[0],meaning:meanings.join('；'),evidence_ids:Array.from(new Set(relevant.flatMap(item=>item.evidence_ids)))}:undefined;
 const generalMeaning=occurrence ? formulaBuiltinMeaning(occurrence.latex,zh?'zh':'en') : undefined;
 const citations=definition?Array.from(new Set(definition.evidence_ids)).map(value=>evidence.find(item=>item.id===value)).filter((item):item is FormulaEvidence=>!!item):[];
 useEffect(()=>{
  const container=root.current;if(!container)return;
  // KaTeX hides its visual HTML in favor of MathML. Expose this controlled
  // interactive rendering, while retaining the complete equation in MathML.
  container.querySelector('.katex-html')?.removeAttribute('aria-hidden');
  model.occurrences.forEach((item,index)=>{
   const target=container.querySelector<HTMLElement>(`.pdfsym-${id}-${index}`);if(!target)return;
   target.classList.add('pdf-interactive-symbol');
   target.dataset.formulaSymbolIndex=String(index);
   target.setAttribute('role','button');target.tabIndex=0;
   target.setAttribute('aria-label',`${label('symbolTarget','Symbol:','符号：')} ${item.latex}`);
   target.setAttribute('aria-pressed',String(active===index));
   target.classList.toggle('is-selected',active===index);
  });
 // The locale changes the accessible label; source/selection changes its targets.
 // eslint-disable-next-line react-hooks/exhaustive-deps
 },[rendered,model,id,active,i18n.resolvedLanguage]);
 useEffect(()=>{setCopyStatus('');},[sourceKey,latex]);
 useEffect(()=>{if(active!==undefined)card.current?.focus({preventScroll:true});},[active,key]);
 const select=(target:EventTarget | null)=>{
  if(!(target instanceof Element))return;
  const element=target.closest<HTMLElement>('[data-formula-symbol-index]');
  if(element && root.current?.contains(element))setSelection({key,index:Number(element.dataset.formulaSymbolIndex)});
 };
 return <div className="pdf-interactive-formula">
  <p className="pdf-symbol-hint">{label('symbolHint','Click a symbol in the formula to inspect its meaning and source.','点击公式中的符号，查看含义与原文依据。')}</p>
  {rendered?<div ref={root} className="pdf-study-preview pdf-interactive-equation" onClick={event=>select(event.target)} onKeyDown={event=>{
   if(event.key==='Enter'||event.key===' '){event.preventDefault();select(event.target);}
   else if(['ArrowLeft','ArrowRight','Home','End'].includes(event.key)){
    const controls=Array.from(root.current?.querySelectorAll<HTMLElement>('[data-formula-symbol-index]') || []);
    const current=controls.indexOf(event.target as HTMLElement);if(current<0)return;
    const next=event.key==='Home'?0:event.key==='End'?controls.length-1:Math.max(0,Math.min(controls.length-1,current+(event.key==='ArrowRight'?1:-1)));
    event.preventDefault();controls[next]?.focus();
   }
  }} dangerouslySetInnerHTML={{__html:rendered}}/>:<pre>{latex}</pre>}
  {(!model.safe || !rendered)&&<p className="pdf-symbol-unavailable">{label('symbolUnsupported','Interactive symbols are unavailable for this formula syntax. The original LaTeX is preserved.','此公式语法暂不支持符号交互，原始 LaTeX 已保留。')}</p>}
  {occurrence&&<div ref={card} tabIndex={-1} role="region" aria-label={label('selectedSymbol','Selected symbol','所选符号')} className="pdf-symbol-definition" onKeyDown={event=>{if(event.key==="Escape"){event.stopPropagation();setSelection(undefined);root.current?.querySelector<HTMLElement>(`.pdfsym-${id}-${active}`)?.focus();}}}>
   {!!parent?.children?.length&&<div className="pdf-symbol-parts" aria-label={zh?'表达式与内部项':'Expression and inner terms'}>{[parent,...parent.children].map((part,index)=><button key={`${part.start}:${part.end}`} type="button" aria-label={`${zh?'查看项：':'Inspect term:'} ${part.latex}`} aria-pressed={occurrence===part} onClick={()=>setSelection({key,index:active!,child:index===0?undefined:index-1})}><FormulaMathText text={part.latex} mathOnly/></button>)}</div>}
   <div className="pdf-symbol-definition-heading"><strong><FormulaMathText text={occurrence.latex} mathOnly/></strong><button aria-label={label('closeSymbol','Close symbol definition','关闭符号释义')} onClick={()=>{setSelection(undefined);root.current?.querySelector<HTMLElement>(`.pdfsym-${id}-${active}`)?.focus();}}>×</button></div>
   {definition?<><p><FormulaMathText text={definition.meaning}/></p>{!compactEvidence&&definition.symbol!==occurrence.latex&&<small>{label('matchedExpression','Matched expression:','匹配表达式：')} <FormulaMathText text={definition.symbol} mathOnly/></small>}{!compactEvidence&&<div className="pdf-symbol-evidence">{citations.map(item=><div key={item.id}><button className="pdf-study-citation" onClick={()=>onNavigate(item)}>{t('pdfFormula.sourcePage',{page:item.page+1})}</button><blockquote>{item.text}</blockquote></div>)}</div>}</>:generalMeaning?<><p>{generalMeaning}</p></>:<p>{label('symbolUnavailable','No explanation is available for this term. Regenerate to try again.','此项暂无释义，可重新生成补全。')}</p>}
  </div>}
  {!compactEvidence&&!!model.unmatchedDefinitions.length&&<details className="pdf-symbol-unmatched"><summary>{label('unmatchedSymbols','Definitions not located in this formula','未在当前公式中定位的释义')} ({model.unmatchedDefinitions.length})</summary><p>{label('unmatchedSymbolsHint','These definitions may concern nearby formulas. No occurrence has been assumed.','这些释义可能属于邻近公式，未推测其对应位置。')}</p>{model.unmatchedDefinitions.map(index=><div key={index}><FormulaMathText text={definitions[index].symbol} mathOnly/></div>)}</details>}
  <details className="pdf-symbol-raw"><summary>{t('pdfFormula.raw')}</summary>{originalLatex&&originalLatex!==latex&&<><strong>{t('pdfFormula.repairedSource')}</strong><pre>{originalLatex}</pre><strong>{t('pdfFormula.displaySource')}</strong></>}<pre>{latex}</pre><button onClick={async()=>{try{await navigator.clipboard.writeText(latex);setCopyStatus(t('pdfFormula.copied'));}catch{setCopyStatus(t('pdfFormula.copyError'));}}}>{t('pdfFormula.copy')}</button>{copyStatus&&<span role="status">{copyStatus}</span>}</details>
 </div>;
}
