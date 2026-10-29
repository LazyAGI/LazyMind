import {useEffect,useId,useMemo,useRef,useState} from 'react';
import {useTranslation} from 'react-i18next';
import katex from 'katex';
import type {FormulaEvidence,FormulaExplanation} from '@/modules/knowledge/api/formulaExplanation';
import {FormulaMathText} from './FormulaMathText';
import {interactiveFormulaSymbols,normalizeFormulaSymbol} from './interactiveFormulaSymbols';
interface Props {latex:string;sourceKey:string;definitions:FormulaExplanation['symbols'];evidence:FormulaEvidence[];onNavigate:(evidence:FormulaEvidence)=>void;resolveSymbol?:(symbol:string,signal:AbortSignal,force?:boolean)=>Promise<FormulaExplanation>}
export function InteractiveFormula({latex,sourceKey,definitions,evidence,onNavigate,resolveSymbol}:Props){
 const {t,i18n}=useTranslation();
 const zh=(i18n.resolvedLanguage || i18n.language).startsWith('zh');
 const label=(key:string,en:string,cn:string)=>t(`pdfFormula.${key}`,{defaultValue:zh?cn:en});
 const id=useId().replace(/[^a-zA-Z0-9]/g,'');
 const root=useRef<HTMLDivElement>(null);
 const card=useRef<HTMLDivElement>(null);
 const key=`${sourceKey}:${latex}:${JSON.stringify(definitions)}`;
 const [selection,setSelection]=useState<{key:string;index:number}>();
 const [copyStatus,setCopyStatus]=useState('');
 const model=useMemo(()=>interactiveFormulaSymbols(latex,definitions.map(definition=>definition.symbol)),[latex,definitions]);
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
 const occurrence=active===undefined?undefined:model.occurrences[active];
 const knownDefinition=occurrence?.definitionIndices.length===1?definitions[occurrence.definitionIndices[0]]:undefined;
 const focusSymbol=model.safe && occurrence?.definitionIndices.length===0 && occurrence.latex.length<=500?occurrence.latex:undefined;
 const lookupKey=focusSymbol?`${key}:${focusSymbol}`:'';
 const cacheScope=`${sourceKey}:${latex}:${zh}`;
 const lookupCache=useMemo(()=>new Map<string,FormulaExplanation>(),[cacheScope]);
 const forceLookup=useRef(false);
 const [lookupRevision,setLookupRevision]=useState(0);
 const [lookup,setLookup]=useState<{key:string;status:'loading'|'ready'|'error';data?:FormulaExplanation;error?:string}>();
 useEffect(()=>{
  if(!focusSymbol || !resolveSymbol)return;
  const normalized=normalizeFormulaSymbol(focusSymbol);
  const force=forceLookup.current;forceLookup.current=false;
  const cached=lookupCache.get(normalized);
  if(cached&&!force){setLookup({key:lookupKey,status:'ready',data:cached});return;}
  const request=new AbortController();
  setLookup({key:lookupKey,status:'loading'});
  void resolveSymbol(focusSymbol,request.signal,force).then(data=>{
   if(request.signal.aborted)return;
   lookupCache.set(normalized,data);setLookup({key:lookupKey,status:'ready',data});
  }).catch(error=>{
   if(request.signal.aborted)return;
   const detail=error?.response?.data?.data?.error_message;
   setLookup({key:lookupKey,status:'error',error:typeof detail==='string'?detail:undefined});
  });
  return()=>request.abort();
 },[focusSymbol,lookupKey,resolveSymbol,lookupCache,lookupRevision]);
 const focused=lookup?.key===lookupKey?lookup:undefined;
 const matched=focused?.data?.symbols.filter(symbol=>normalizeFormulaSymbol(symbol.symbol)===normalizeFormulaSymbol(focusSymbol || '')) || [];
 const definition=knownDefinition || (matched.length===1?matched[0]:undefined);
 const citationEvidence=knownDefinition?evidence:focused?.data?.evidence || [];
 const citations=definition?Array.from(new Set(definition.evidence_ids)).map(value=>citationEvidence.find(item=>item.id===value)).filter((item):item is FormulaEvidence=>!!item):[];
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
  {occurrence&&<div ref={card} tabIndex={-1} role="region" aria-label={label('selectedSymbol','Selected symbol','所选符号')} className="pdf-symbol-definition" onKeyDown={event=>{if(event.key==="Escape"){setSelection(undefined);root.current?.querySelector<HTMLElement>(`.pdfsym-${id}-${active}`)?.focus();}}}>
   <div className="pdf-symbol-definition-heading"><strong><FormulaMathText text={occurrence.latex} mathOnly/></strong><button aria-label={label('closeSymbol','Close symbol definition','关闭符号释义')} onClick={()=>{setSelection(undefined);root.current?.querySelector<HTMLElement>(`.pdfsym-${id}-${active}`)?.focus();}}>×</button></div>
   {definition?<><p><FormulaMathText text={definition.meaning}/></p>{definition.symbol!==occurrence.latex&&<small>{label('matchedExpression','Matched expression:','匹配表达式：')} <FormulaMathText text={definition.symbol} mathOnly/></small>}{(definition.inferred || !citations.length)&&<span className="pdf-study-inferred">{t('pdfFormula.inferred')}</span>}<div className="pdf-symbol-evidence">{citations.map(item=><div key={item.id}><button className="pdf-study-citation" onClick={()=>onNavigate(item)}>{t('pdfFormula.sourcePage',{page:item.page+1})}</button><blockquote>{item.text}</blockquote></div>)}</div></>:<>{focused?.status==='loading'?<p role="status">{label('symbolLookingUp','Looking up this symbol in the paper…','正在结合论文查找此符号的释义…')}</p>:focused?.status==='error'?<p role="alert">{focused.error || label('symbolLookupError','Symbol lookup failed. Please retry.','符号释义查询失败，请重试。')}</p>:<p>{occurrence.definitionIndices.length>1?label('symbolAmbiguous','Multiple definitions match this symbol; a meaning cannot be selected reliably.','此符号匹配多个释义，无法可靠选择含义。'):label('symbolUnavailable','No verified definition is available for this symbol.','此符号暂无可核对的释义。')}</p>}{focusSymbol&&resolveSymbol&&focused?.status!=='loading'&&<button onClick={()=>{forceLookup.current=true;setLookupRevision(value=>value+1);}}>{label('symbolLookupRetry','Retry symbol lookup','重试符号释义')}</button>}</>}
  </div>}
  {!!model.unmatchedDefinitions.length&&<details className="pdf-symbol-unmatched"><summary>{label('unmatchedSymbols','Definitions not located in this formula','未在当前公式中定位的释义')} ({model.unmatchedDefinitions.length})</summary><p>{label('unmatchedSymbolsHint','These definitions may concern nearby formulas. No occurrence has been assumed.','这些释义可能属于邻近公式，未推测其对应位置。')}</p>{model.unmatchedDefinitions.map(index=><div key={index}><FormulaMathText text={definitions[index].symbol} mathOnly/></div>)}</details>}
  <details className="pdf-symbol-raw"><summary>{t('pdfFormula.raw')}</summary><pre>{latex}</pre><button onClick={async()=>{try{await navigator.clipboard.writeText(latex);setCopyStatus(t('pdfFormula.copied'));}catch{setCopyStatus(t('pdfFormula.copyError'));}}}>{t('pdfFormula.copy')}</button>{copyStatus&&<span role="status">{copyStatus}</span>}</details>
 </div>;
}
