import {isBuiltinFormulaSymbol} from './formulaSymbolGlossary';
export interface FormulaSymbolOccurrence {start:number;end:number;latex:string;definitionIndices:number[];children?:FormulaSymbolOccurrence[]}
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

export function normalizeFormulaSymbol(value:string){
 if(!withinSymbolBudget(value))return "";
 const input=stripDelimiters(value)
  .replace(/'+/g, primes=>'^{'+String.raw`\prime`.repeat(primes.length)+'}')
  .replace(/\\[,!;: ]|\\(?:quad|qquad|enspace|thinspace|medspace|thickspace)\b/g,'')
  .replace(/\\(?:left|right|big[lrm]?|Big[lrm]?|bigg[lrm]?|Bigg[lrm]?)\b/g,'')
  .replace(/\\(?:lVert|rVert|Vert)(?![A-Za-z])|\\\|/g,'‖')
  .replace(/\\(?:lvert|rvert|vert)(?![A-Za-z])/g,'|');let cursor=0;
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
   if(['frac','dfrac','tfrac','binom','dbinom','tbinom'].includes(command[1])){
    const name=command[1].includes('binom')?'binom':'frac';
    return '\\'+name+'('+argument()+')('+argument()+')';
   }
   if(['overset','underset','stackrel'].includes(command[1]))return command[0]+'('+argument()+')('+argument()+')';
   if(command[1]==='sqrt'){
    skip();let degree='';
    if(input[cursor]==='['){cursor++;const start=cursor;while(cursor<input.length&&input[cursor]!==']')cursor++;degree=normalizeFormulaSymbol(input.slice(start,cursor));if(input[cursor]===']')cursor++;}
    return '\\sqrt['+degree+']('+argument()+')';
   }
   if(['mathrm','mathit','mathsf','text','operatorname'].includes(command[1]))return argument();
   if(['sin','cos','tan','log','ln','exp'].includes(command[1]))return command[1];
   return wrappers.has(command[1])?command[0]+'('+argument()+')':command[0]+';';
  }
  return input[cursor++];
 };
 return sequence();
}

export function interactiveFormulaSymbols(latex:string,definitions:string[],includeBuiltins=false) {
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
   if((structural.has(name)&&!(includeBuiltins&&isBuiltinFormulaSymbol(command[0])&&!['sqrt','frac','dfrac','tfrac'].includes(name))) || /^(?:lVert|rVert|Vert|lvert|rvert|vert)$/.test(name) || !/^[a-zA-Z]+$/.test(name)){i+=command[0].length;continue;}
  }else if(!/[a-zA-Z0-9\u0370-\u03ff]/.test(latex[i])&&!(includeBuiltins&&isBuiltinFormulaSymbol(latex[i]))){i++;continue;}
  const end=atomEnd(i);
  if(occurrences.length>=512)return empty;
  occurrences.push({start:i,end,latex:latex.slice(i,end),definitionIndices:[]});i=end;
 }
 if(!definitions.length&&!includeBuiltins)return {safe:true,occurrences,unmatchedDefinitions:[]};
 // Build candidate source ranges BEFORE resolving explanations. Structural
 // commands retain all arguments; child sequences retain their own ranges.
 // This replaces symbol-specific call/norm/string-concatenation repair passes.
 interface Node {start:number;end:number;children:Node[][];boundary?:boolean;baseEnd?:number}
 const sequences:Node[][]=[];
 let nodeCount=0,overBudget=false;
 const arity:Record<string,number>={frac:2,dfrac:2,tfrac:2,binom:2,dbinom:2,tbinom:2,sqrt:1,overset:2,underset:2,stackrel:2};
 const read=(offset:number,limit:number):Node=>{
  const start=skip(offset);let end=start+1,baseEnd:number|undefined;const children:Node[][]=[];
  if(latex[start]==='{'){
   end=groupEnd(start);if(end<=start)end=start+1;
   children.push(scan(start+1,end-1));
  }else if(latex[start]==='\\'){
   const command=latex.slice(start).match(/^\\([a-zA-Z]+|.)/);
   if(!command)return {start,end,children};
   const name=command[1];end=start+command[0].length;
   if(['begin','end','tag','label'].includes(name)){
    let next=skip(end);if(latex[next]==='*')next=skip(next+1);
    end=groupEnd(next)||next;
    if(name==='begin'&&latex.slice(next+1,end-1)==='array'){const format=skip(end);end=groupEnd(format)||end;}
    return {start,end,children,boundary:true};
   }
   if(name==='\\')return {start,end,children,boundary:true};
   if(/^(?:left|right|big[lrm]?|Big[lrm]?|bigg[lrm]?|Bigg[lrm]?)$/.test(name)){
    const delimiter=skip(end);const token=latex.slice(delimiter).match(/^\\(?:[A-Za-z]+|.)|^./);
    end=delimiter+(token?.[0].length||0);
   }else if(arity[name]){
    if(name==='sqrt'&&latex[skip(end)]==='['){
     const degree=skip(end);const close=latex.indexOf(']',degree+1);
     if(close<0||close>=limit)return {start,end,children,boundary:true};
     children.push(scan(degree+1,close));end=close+1;
    }
    for(let n=0;n<arity[name];n++){
     const arg=read(skip(end),limit);end=arg.end;
     if(latex[arg.start]==='{')children.push(...arg.children);else children.push([arg]);
    }
   }else if(wrappers.has(name)){
    // Formatting and accents belong to their identifier, not separate letters.
    end=atomEnd(start);
   }
  }
  // Scripts are attached to the complete base. Expose expression exponents,
  // but keep label-like subscripts opaque (e.g. d_model, PE_(pos,2i)).
  const baseSource=latex.slice(start,end);
  if(/^(?:\\(?:left|right|big[lrm]?|Big[lrm]?|bigg[lrm]?|Bigg[lrm]?)\s*)?(?:[)\]|‖]|\\(?:\||[lr]?Vert|[lr]?vert))$/.test(baseSource)||/^\\(?:sqrt|[dt]?frac|[dt]?binom)\b/.test(baseSource))baseEnd=end;
  while(end<limit){
   const script=skip(end);if(latex[script]!=='_'&&latex[script]!=='^')break;
   const argStart=skip(script+1);
   const argEnd=latex[argStart]==='{'?groupEnd(argStart):atomEnd(argStart);
   if(argEnd<=argStart)break;
   if(latex[script]==='^'&&complexExponent(latex.slice(argStart,argEnd))){
    baseEnd??=end;
    children.push(scan(latex[argStart]==='{'?argStart+1:argStart,latex[argStart]==='{'?argEnd-1:argEnd));
   }
   end=argEnd;
  }
  return {start,end,children,baseEnd,boundary:latex[start]==='&'};
 };
 const scan=(from:number,to:number):Node[]=>{
  const nodes:Node[]=[];
  for(let i=skip(from);i<to;){
   if(++nodeCount>2048){overBudget=true;break;}
   const node=read(i,to);if(node.end<=i||node.end>to){overBudget=true;break;}
   nodes.push(node);i=skip(node.end);
  }
  sequences.push(nodes);return nodes;
 };
 scan(0,latex.length);if(overBudget)return empty;
 const normalized=definitions.map(normalizeFormulaSymbol);
 const byKey=new Map<string,number[]>();
 normalized.forEach((value,index)=>{if(value)byKey.set(value,[...(byKey.get(value)||[]),index]);});
 const matchedRanges:FormulaSymbolOccurrence[]=[];
 const builtinRanges:FormulaSymbolOccurrence[]=[];
 for(const atom of occurrences){const indices=byKey.get(normalizeFormulaSymbol(atom.latex));if(indices)matchedRanges.push({...atom,definitionIndices:indices});}
 // Structural operators require their complete arguments inside the clickable
 // range: wrapping a bare \\sqrt or \\frac would change/break TeX parsing.
 if(includeBuiltins)for(const nodes of sequences)for(const node of nodes){
  const source=latex.slice(node.start,node.end);
  if(/^\\(?:sqrt|[dt]?frac)\b/.test(source)&&isBuiltinFormulaSymbol(source))builtinRanges.push({start:node.start,end:node.end,latex:source,definitionIndices:[]});
 }
 // Complete balanced sibling ranges cover calls, products and delimiters as
 // well as single structural nodes. Never cross arguments or equation rows.
 const balanced=(value:string)=>{
  const tokens=value.match(/\\[A-Za-z]+|\\.|[()[\]|‖]/g)||[];
  const stack:string[]=[];
  for(const token of tokens){
   if(token==='('||token==='[')stack.push(token);
   else if(token===')'||token===']'){if(stack.pop()!==(token===')'?'(':'['))return false;}
   else if(/^(?:\||‖|\\(?:\||[lr]?Vert|[lr]?vert))$/.test(token)){
    const kind=/Vert|‖|\\\|/.test(token)?'‖':'|';
    if(stack[stack.length-1]===kind)stack.pop();else stack.push(kind);
   }
  }
  return stack.length===0;
 };
 let comparisons=0,matchedCharacters=0;
 for(const nodes of sequences){
  for(let i=0;i<nodes.length;i++){
   if(nodes[i].boundary)continue;
   for(let j=i;j<Math.min(nodes.length,i+64);j++){
    if(nodes[j].boundary)break;
    if(++comparisons>32768)return empty;
    for(const end of new Set([nodes[j].end,nodes[j].baseEnd].filter((value):value is number=>value!==undefined))){
     const start=nodes[i].start,source=latex.slice(start,end);
     matchedCharacters+=source.length;if(matchedCharacters>2_000_000)return empty;
     if(end<=start||!balanced(source))continue;
     const indices=byKey.get(normalizeFormulaSymbol(source));
     if(indices)matchedRanges.push({start,end,latex:source,definitionIndices:indices});
    }
   }
  }
 }
 // Prefer complete explained expressions. Their explained descendants remain
 // available in the definition card, never as competing nested DOM buttons.
 // Generic fraction/root help must not displace a model-explained numerator,
 // denominator or complete term. It fills only otherwise unexplained ranges.
 matchedRanges.push(...builtinRanges.filter(item=>!matchedRanges.some(explained=>explained.start<item.end&&explained.end>item.start)));
 const unique=new Map<string,FormulaSymbolOccurrence>();
 for(const item of matchedRanges)unique.set(`${item.start}:${item.end}`,item);
 const ordered=[...unique.values()].sort((a,b)=>(b.end-b.start)-(a.end-a.start)||a.start-b.start);
 const selected:FormulaSymbolOccurrence[]=[];
 for(const item of ordered){
  if(selected.some(parent=>parent.start<item.end&&parent.end>item.start))continue;
  item.children=ordered.filter(child=>child!==item&&child.start>=item.start&&child.end<=item.end)
   .sort((a,b)=>a.start-b.start||b.end-a.end);
  if(includeBuiltins)for(const atom of occurrences){
   if(atom.start>=item.start&&atom.end<=item.end&&(atom.start!==item.start||atom.end!==item.end)&&isBuiltinFormulaSymbol(atom.latex)&&!item.children.some(child=>child.start===atom.start&&child.end===atom.end))item.children.push(atom);
  }
  selected.push(item);
 }
 occurrences=occurrences.filter(atom=>!selected.some(item=>item.start<atom.end&&item.end>atom.start));
 occurrences.push(...selected);
 occurrences.sort((a,b)=>a.start-b.start);
 const matched=new Set(occurrences.flatMap(o=>[...o.definitionIndices,...(o.children||[]).flatMap(child=>child.definitionIndices)]));
 return {safe:true,occurrences,unmatchedDefinitions:definitions.map((_,index)=>index).filter(index=>!matched.has(index))};
}
