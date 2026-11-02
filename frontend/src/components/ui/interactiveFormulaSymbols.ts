export interface FormulaSymbolOccurrence {start:number;end:number;latex:string;definitionIndices:number[]}
const wrappers=new Set(['mathcal','mathbb','mathbf','mathsf','mathrm','mathit','mathfrak','boldsymbol','operatorname','text','hat','widehat','bar','overline','vec','tilde','widetilde','dot','ddot','breve','check','acute','grave','underline']);
const structural=new Set(['frac','dfrac','tfrac','sqrt','sum','prod','int','iint','lim','left','right','big','Big','bigg','Bigg','bigl','bigr','Bigl','Bigr','limits','nolimits','displaystyle','textstyle','scriptstyle','scriptscriptstyle','quad','qquad','hspace','vspace']);
const unsafe=/\\(?:html\w*|href|url|includegraphics|def|gdef|edef|xdef|newcommand|renewcommand|providecommand|let|global|csname)\b/;
// Bound all recursive paths before parsing, including brace-free wrapper/script
// chains. The caller preserves the raw formula when interaction is unavailable.
function withinSymbolBudget(value:string):boolean {
 if(value.length>50000)return false;
 let depth=0,complexity=0;
 for(let index=0;index<value.length;index++){
  const char=value[index];
  if(char==='\\'){if(++complexity>256)return false;index++;continue;}
  if(char==='_'||char==='^'){if(++complexity>256)return false;}
  if(char==='{'){if(++depth>64)return false;}
  else if(char==='}' && --depth<0)return false;
 }
 return depth===0;
}
function stripDelimiters(value:string){return value.replace(/^\s*(?:\$\$?|\\\()/,'').replace(/(?:\$\$?|\\\))\s*$/,'');}
function looseSymbol(value:string){return stripDelimiters(value).replace(/[\s{}]/g,'');}
// Explanations may name a familiar function without the TeX wrapper used to
// typeset it. Keep this alias restricted to multi-letter named operators.
function namedOperator(value:string){
 const match=value.trim().match(/^\\(?:operatorname|mathrm|mathsf|text)\s*\{\s*([A-Za-z][A-Za-z0-9]+)\s*\}$/);
 return match?.[1] || '';
}
export function normalizeFormulaSymbol(value:string){
 if(!withinSymbolBudget(value))return "";
 const input=stripDelimiters(value).replace(/\\(?:left|right)\b/g,'');let cursor=0;
 const skip=()=>{while(cursor<input.length && /\s/.test(input[cursor]))cursor++;};
 const sequence=(group=false):string=>{
  let result='';
  while(cursor<input.length){skip();if(group&&input[cursor]==='}'){cursor++;break;}if(cursor>=input.length)break;
   if(input[cursor]==='_'||input[cursor]==='^'){const script=input[cursor++];result+=script+'('+argument()+')';}
   else result+=argument();
  }return result;
 };
 const argument=():string=>{
  skip();if(cursor>=input.length)return '';
  if(input[cursor]==='{'){cursor++;return sequence(true);}
  if(input[cursor]==='\\'){
   const command=input.slice(cursor).match(/^\\([a-zA-Z]+|.)/);if(!command){cursor++;return '\\';}
   cursor+=command[0].length;
   if(['mathrm','mathit','mathsf','text','operatorname'].includes(command[1]))return argument();
   if(['sin','cos','tan','log','ln','exp'].includes(command[1]))return command[1];
   return wrappers.has(command[1])?command[0]+'('+argument()+')':command[0]+';';
  }
  return input[cursor++];
 };
 return sequence();
}

export function interactiveFormulaSymbols(latex:string,definitions:string[]) {
 const empty={safe:false,occurrences:[] as FormulaSymbolOccurrence[],unmatchedDefinitions:definitions.map((_,i)=>i)};
 if(unsafe.test(latex) || !withinSymbolBudget(latex))return empty;
 const skip=(offset:number)=>{while(/\s/.test(latex[offset] || '') && offset<latex.length)offset++;return offset;};
 const groupEnd=(offset:number):number=>{
  if(latex[offset]!=='{')return offset;
  let depth=0;
  for(let i=offset;i<latex.length;i++){
   if(latex[i]==='\\'){i++;continue;}
   if(latex[i]==='{')depth++;
   if(latex[i]==='}' && --depth===0)return i+1;
  }
  return offset;
 };
 // Label-like scripts remain part of an identifier (r_n^{q-}, P_i^{i2t}).
 // An exponent expression must instead expose its own semantic atoms. This
 // decision uses only source syntax, never a model definition's interpretation.
 const complexExponent=(value:string):boolean=>{
  const compact=value.replace(/[\s{}]/g,'');
  return /[=/*(),]/.test(compact) || /[+-]/.test(compact.slice(0,-1)) || /\\(?:frac|dfrac|tfrac|sqrt|sum|prod|int|iint|sim|cdot|times|log|exp|sin|cos|left|right)\b/.test(value);
 };
 const atomEnd=(offset:number):number=>{
  let end=offset;
  if(latex[offset]==='\\'){
   const command=latex.slice(offset).match(/^\\([a-zA-Z]+|.)/);
   if(!command)return offset+1;
   end=offset+command[0].length;
   if(wrappers.has(command[1])){
    const next=skip(end);
    if(latex[next]==='{')end=groupEnd(next);
    else end=atomEnd(next);
   }
  }else if(latex[offset]==='{')end=groupEnd(offset);
  else end=offset+1;
  if(end<=offset)return offset+1;
  while(true){
   const script=skip(end);
   if(latex[script]!=='_' && latex[script]!=='^')break;
   const value=skip(script+1);
   const scriptEnd=latex[value]==='{'?groupEnd(value):atomEnd(value);
   if(latex[script]==='^' && complexExponent(latex.slice(value,scriptEnd)))break;
   end=scriptEnd;
   if(end<=value)break;
  }
  return end;
 };
 let occurrences:FormulaSymbolOccurrence[]=[];
 for(let i=0;i<latex.length;){
  i=skip(i);if(i>=latex.length)break;
  if(latex[i]==='\\'){
   const command=latex.slice(i).match(/^\\([a-zA-Z]+|.)/);
   if(!command)return empty;
   const name=command[1];
   if(['begin','end','tag','label'].includes(name)){
    let next=skip(i+command[0].length);if(latex[next]==='*')next=skip(next+1);
    const envEnd=groupEnd(next);const env=latex.slice(next+1,envEnd-1);
    i=envEnd>next?envEnd:next;
    if(name==='begin' && env==='array'){const format=skip(i);i=groupEnd(format)||i;}
    continue;
   }
   if(structural.has(name) || !/^[a-zA-Z]+$/.test(name)){i+=command[0].length;continue;}
  }else if(!/[a-zA-Z0-9\u0370-\u03ff]/.test(latex[i])){i++;continue;}
  const end=atomEnd(i);
  if(occurrences.length>=512)return empty;
  occurrences.push({start:i,end,latex:latex.slice(i,end),definitionIndices:[]});i=end;
 }
 // Match complete semantic atoms first; formatting whitespace and redundant
 // braces may differ, but accents, commands and script structure stay distinct.
 const normalized=definitions.map(normalizeFormulaSymbol);
 occurrences.forEach(occurrence=>{const value=normalizeFormulaSymbol(occurrence.latex);const alias=namedOperator(occurrence.latex);normalized.forEach((definition,index)=>{if(value===definition || (alias && alias===definition))occurrence.definitionIndices.push(index);});});
 // Keep original source offsets while matching typography-independent names.
 let canonical='';const offsets:number[]=[];
 for(let i=0;i<latex.length;i++)if(!/[\s{}]/.test(latex[i])){canonical+=latex[i];offsets.push(i);}
 // Group a multi-letter identifier only when the supplied definition exactly
 // matches a complete contiguous source range; never merge partial subscripts.
 definitions.map(value=>withinSymbolBudget(value)?looseSymbol(value):"").forEach((definition,index)=>{
  const indexedName=/^[A-Za-z]{2,}_/.test(definition);
  if(!definition || (!indexedName && /[(),=+*/^-]/.test(definition)))return;
  let from=0;
  while(true){
   const found=canonical.indexOf(definition,from);if(found<0)break;from=found+definition.length;
   const start=offsets[found],lastOffset=offsets[found+definition.length-1];
   const first=occurrences.find(o=>o.start===start);
   const last=occurrences.find(o=>o.start<=lastOffset&&o.end>lastOffset);
   if(!first || !last)continue;
   if(first===last){if(normalizeFormulaSymbol(first.latex)===normalized[index]&&!first.definitionIndices.includes(index))first.definitionIndices.push(index);continue;}
   const end=last.end;
   if(normalizeFormulaSymbol(latex.slice(start,end))!==normalized[index])continue;
   occurrences=occurrences.filter(o=>o.end<=start||o.start>=end);
   occurrences.push({start,end,latex:latex.slice(start,end),definitionIndices:[index]});
  }
 });
 // A returned call definition owns its complete source expression, including
 // arguments. Match larger calls first so nested terms cannot fragment them.
 // Never synthesize LaTeX or make independently nested click targets.
 normalized.map((value,index)=>({value,index})).filter(item=>stripDelimiters(definitions[item.index]).trim().endsWith(')'))
  .sort((a,b)=>b.value.length-a.value.length).forEach(({value,index})=>{
   for(const first of [...occurrences]){
    if(!occurrences.includes(first))continue;
    const head=normalizeFormulaSymbol(first.latex);
    if(!head || !value.startsWith(head))continue;
    for(let end=first.end;end<=latex.length;end++){
     if(latex[end-1]!==')')continue;
     const source=latex.slice(first.start,end);
     // A group must stay inside one TeX row and be syntactically complete.
     if(/\\\\|&|\\(?:begin|end)\b/.test(source))break;
     const candidate=normalizeFormulaSymbol(source);
     if(candidate!==value){
      // Extending an already complete, equally long expression cannot make
      // it match; avoid scanning the rest of a long equation for every atom.
      if(candidate.length>=value.length)break;
      continue;
     }
     if(occurrences.some(item=>item.start<end && item.end>end))continue;
     const existing=occurrences.find(item=>item.start===first.start&&item.end===end);
     if(existing){if(!existing.definitionIndices.includes(index))existing.definitionIndices.push(index);}
     else {
      occurrences=occurrences.filter(item=>item.end<=first.start||item.start>=end);
      occurrences.push({start:first.start,end,latex:source,definitionIndices:[index]});
     }
     break;
    }
   }
  });
 // Model-provided semantic terms define groups, but only after matching a
 // complete source range. Preserve original TeX and ignore typography only.
 definitions.forEach((_,definitionIndex)=>{
  if(occurrences.some(item=>item.definitionIndices.includes(definitionIndex)))return;
  const expected=normalized[definitionIndex];if(!expected)return;
  const ordered=[...occurrences].sort((a,b)=>a.start-b.start);
  for(let i=0;i<ordered.length;i++){
   for(let j=i;j<Math.min(ordered.length,i+24);j++){
    const start=ordered[i].start,end=ordered[j].end;
    if(normalizeFormulaSymbol(latex.slice(start,end))!==expected)continue;
    const existing=occurrences.find(item=>item.start===start&&item.end===end);
    if(existing)existing.definitionIndices.push(definitionIndex);
    else {
     occurrences=occurrences.filter(item=>item.end<=start||item.start>=end);
     occurrences.push({start,end,latex:latex.slice(start,end),definitionIndices:[definitionIndex]});
    }
    break;
   }
  }
 });
 occurrences.sort((a,b)=>a.start-b.start);
 const matched=new Set(occurrences.flatMap(o=>o.definitionIndices));
 return {safe:true,occurrences,unmatchedDefinitions:definitions.map((_,index)=>index).filter(index=>!matched.has(index))};
}
