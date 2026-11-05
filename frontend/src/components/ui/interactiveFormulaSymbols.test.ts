import {describe,it,expect} from 'vitest';
import {interactiveFormulaSymbols,normalizeFormulaSymbol} from './interactiveFormulaSymbols';
const sps=String.raw`\mathcal {L} (I _ {i}, T _ {i}) = \mathcal {H} (y _ {i}, \mathcal {P} _ {i} ^ {i 2 t}) + \mathcal {H} (y _ {i}, \mathcal {P} _ {i} ^ {t 2 i}),\tag{1}`;
const cc=String.raw`\begin{array}{c} \mathcal {L} _ {c c} = \sum_ {i = 1} ^ {l} \operatorname{ReLU} \big (C r o s s A t t n (I, T _ {N P _ {i}}) \\ - C r o s s A t t n (I, N P _ {i}) - \epsilon \big) \end{array}`;
it.each(['x\\', 'x\\\ny'])('rejects malformed command tokens without throwing: %j',latex=>{
 expect(interactiveFormulaSymbols(latex,[])).toMatchObject({safe:false,occurrences:[]});
});
describe('deterministic complete formula symbol matching',()=>{
 it('matches the real SPS spacing/scripts and groups full function definitions',()=>{
 const result=interactiveFormulaSymbols(sps,[String.raw`\mathcal{L}(I_i,T_i)`,String.raw`\mathcal{H}`,String.raw`y_i`,String.raw`\mathcal{P}_{i}^{i 2 t}`,String.raw`\tau`]);
 expect(result.occurrences.filter(o=>o.definitionIndices.includes(0)).map(o=>o.latex)).toEqual([String.raw`\mathcal {L} (I _ {i}, T _ {i})`]);
 expect(result.occurrences.filter(o=>o.definitionIndices.includes(1))).toHaveLength(2);
 expect(result.occurrences.filter(o=>o.definitionIndices.includes(2))).toHaveLength(2);
 expect(result.occurrences.find(o=>o.definitionIndices.includes(3))?.latex).toBe(String.raw`\mathcal {P} _ {i} ^ {i 2 t}`);
 expect(result.unmatchedDefinitions).toEqual([4]);
 expect(result.occurrences.find(o=>o.latex==='I _ {i}')?.definitionIndices).toBeUndefined();
 });
 it('matches both real CC CrossAttn calls separately and keeps nested subscripts inside each call',()=>{
 const result=interactiveFormulaSymbols(cc,[String.raw`\mathcal{L}_{cc}`,'i',String.raw`\operatorname{ReLU}`,'CrossAttn(I, T_{NP_{i}})','CrossAttn(I, NP_{i})',String.raw`\epsilon`,'l']);
 expect(result.unmatchedDefinitions).toEqual([]);
 expect(result.occurrences.filter(o=>o.latex.startsWith('C r o s s A t t n (')).map(o=>o.definitionIndices)).toEqual([[3],[4]]);
 expect(result.occurrences.some(o=>o.latex==='T _ {N P _ {i}}')).toBe(false);
 });
 it('keeps accents and subscripts whole, distinguishes ambiguous definitions, and never matches inside commands',()=>{
 const result=interactiveFormulaSymbols(String.raw`\hat {\mathbf {x}} _ i + \xi`,[String.raw`\hat{\mathbf{x}}_{i}`,String.raw`\hat{\mathbf{x}}_i`,'i']);
 expect(result.occurrences[0].definitionIndices).toEqual([0,1]);
 expect(result.unmatchedDefinitions).toContain(2);
 });
 it('matches new atomic function and multi-letter argument definitions',()=>{
 const result=interactiveFormulaSymbols(cc,['CrossAttn','NP_i','I',String.raw`T_{NP_i}`]);
 expect(result.unmatchedDefinitions).toEqual([]);
 expect(result.occurrences.filter(o=>o.definitionIndices.includes(0))).toHaveLength(2);
 });
 it('matches named operators when the explanation uses their plain names',()=>{
  const formula=String.raw`\operatorname{Attention}(Q,K,V)+\mathrm{softmax}(QK^T)`;
  const result=interactiveFormulaSymbols(formula,['Attention','softmax','Q','K','V']);
  expect(result.unmatchedDefinitions).toEqual([]);
  expect(result.occurrences.find(o=>o.latex===String.raw`\operatorname{Attention}`)?.definitionIndices).toEqual([0]);
  expect(result.occurrences.find(o=>o.latex===String.raw`\mathrm{softmax}`)?.definitionIndices).toEqual([1]);
 });
 it('does not confuse accent scope or nested superscripts',()=>{
 expect(interactiveFormulaSymbols(String.raw`\hat{x}_i + x_{i^j}`,[String.raw`\hat{x_i}`,String.raw`x_i^j`]).unmatchedDefinitions).toEqual([0,1]);
 });
 it('disables generated metadata for untrusted HTML, links, or macro definitions',()=>{
 for(const latex of [String.raw`\htmlClass{evil}{x}`,String.raw`\href{javascript:alert(1)}{x}`,String.raw`\def\a{\htmlClass}\a{x}{y}`])expect(interactiveFormulaSymbols(latex,['x']).safe).toBe(false);
 });
});


it('falls back safely for deeply nested, recursive-script, or excessive occurrence input',()=>{
 const nested='\\mathcal{'+'{'.repeat(6000)+'x'+'}'.repeat(6000)+'}';
 expect(interactiveFormulaSymbols(nested,[]).safe).toBe(false);
 expect(interactiveFormulaSymbols('x'+ '_x'.repeat(6000),[]).safe).toBe(false);
 expect(interactiveFormulaSymbols('x+'.repeat(2000),[]).safe).toBe(false);
 expect(()=>interactiveFormulaSymbols('x',[nested])).not.toThrow();
 expect(interactiveFormulaSymbols('x',[nested]).unmatchedDefinitions).toEqual([0]);
});

it('exposes semantic atoms inside complex exponents while preserving indexed variables',()=>{
 const latex=String.raw`\mathcal{L}_{clean}=-\log\frac{e^{\mathrm{sim}(r_n^q,r_n^{q-})/\tau}}{\sum_{j=1}^{N}e^{\mathrm{sim}(r_n^q,r_j^{q+})/\tau}}`;
 const result=interactiveFormulaSymbols(latex,[String.raw`\tau`,String.raw`r_n^q`,String.raw`r_n^{q-}`,String.raw`\mathcal{L}_{clean}`,String.raw`\mathrm{sim}`]);
 expect(result.safe).toBe(true);expect(result.unmatchedDefinitions).toEqual([]);
 expect(result.occurrences.filter(item=>item.latex==='e')).toHaveLength(2);
 expect(result.occurrences.filter(item=>item.latex===String.raw`\tau`)).toHaveLength(2);
 expect(result.occurrences.some(item=>item.latex===String.raw`r_n^{q-}`)).toBe(true);
 expect(result.occurrences.every((item,index)=>index===0||result.occurrences[index-1].end<=item.start)).toBe(true);
});
it('keeps a compound exponent intact and retains its explained children',()=>{
 const latex=String.raw`e^{\frac{x_i}{\tau}}+r_n^{q-}+\mathcal{P}_i^{i2t}`;
 const result=interactiveFormulaSymbols(latex,[String.raw`e^{\frac{x_i}{\tau}}`,String.raw`\tau`,String.raw`x_i`]);
 expect(result.unmatchedDefinitions).toEqual([]);
 expect(result.occurrences[0].latex).toBe(String.raw`e^{\frac{x_i}{\tau}}`);
 expect(result.occurrences[0].children?.map(item=>item.latex)).toEqual(['x_i',String.raw`\tau`]);
});

it('aligns semantic groups despite typesetting differences',()=>{
 const result=interactiveFormulaSymbols(String.raw`P E_{\left(pos,2i\right)}=\sin(x/d_{\mathrm{model}})`,['PE_{(pos,2i)}','sin','d_{model}']);
 expect(result.unmatchedDefinitions).toEqual([]);
 expect(result.occurrences.find(item=>item.definitionIndices.includes(0))?.latex).toBe(String.raw`P E_{\left(pos,2i\right)}`);
});


it.each([
 ['L(w)+L(w)+w',['L(w)','w']],
 [String.raw`L\left(w\right)+w`,['L(w)','w']],
 [String.raw`\operatorname{L}(w)+w`,['L(w)','w']],
 ['L(g(w))+w',['g(w)','L(g(w))','w']],
])('groups complete explained calls and leaves outside variables independent: %s',(latex,definitions)=>{
 const result=interactiveFormulaSymbols(latex,definitions);
 const callIndex=definitions.findIndex(value=>value.startsWith('L('));
 const calls=result.occurrences.filter(item=>item.definitionIndices.includes(callIndex));
 expect(calls.length).toBe(latex==='L(w)+L(w)+w'?2:1);
 expect(calls.every(item=>item.latex.endsWith(')'))).toBe(true);
 expect(result.occurrences.filter(item=>item.latex==='w')).toHaveLength(1);
 expect(result.occurrences.every((item,index)=>index===0||result.occurrences[index-1].end<=item.start)).toBe(true);
});
it('does not attach a call definition to a different argument or a partial script',()=>{
 const result=interactiveFormulaSymbols('L(v)+L(w_i)',['L(w)']);
 expect(result.unmatchedDefinitions).toEqual([0]);
});

it('keeps duplicate call meanings together without creating overlapping targets',()=>{
 const result=interactiveFormulaSymbols('L(w)',['L(w)','L(w)']);
 expect(result.occurrences).toMatchObject([{latex:'L(w)',definitionIndices:[0,1]}]);
 expect(result.occurrences).toHaveLength(1);
});
it('never groups a call across array rows',()=>{
 const result=interactiveFormulaSymbols(String.raw`\begin{array}{c} L(w\\+v) \end{array}`,['L(w+v)']);
 expect(result.unmatchedDefinitions).toEqual([0]);
});

const normBound={"latex": "| \\mathcal {L} (\\mathbf {w} ^ {\\prime}) - \\mathcal {L} (\\mathbf {w}) | \\leq \\| \\nabla_ {\\mathbf {w}} \\mathcal {L} (\\mathbf {w}) \\| \\| \\delta \\mathbf {w} \\| + \\frac {1}{2} \\| H \\| \\| \\delta \\mathbf {w} \\| ^ {2},", "symbols": [{"symbol": "\\mathcal{L}(\\mathbf{w}^{\\prime})", "meaning": "压缩后权重$\\mathbf{w}^{\\prime}$对应的损失函数值。", "evidence_ids": ["66fd4c218a6148fa7d2e23cc"], "inferred": true}, {"symbol": "\\mathcal{L}(\\mathbf{w})", "meaning": "原始权重$\\mathbf{w}$对应的损失函数值。", "evidence_ids": ["66fd4c218a6148fa7d2e23cc"], "inferred": true}, {"symbol": "\\| \\nabla_{\\mathbf{w}} \\mathcal{L}(\\mathbf{w}) \\|", "meaning": "损失函数关于原始权重$\\mathbf{w}$的梯度的范数。", "evidence_ids": ["550077dc6afc93235e6f8b2a"], "inferred": true}, {"symbol": "\\| \\delta \\mathbf{w} \\|", "meaning": "压缩引入的权重扰动$\\delta \\mathbf{w} = \\mathbf{w}^{\\prime} - \\mathbf{w}$的范数。", "evidence_ids": ["66fd4c218a6148fa7d2e23cc", "550077dc6afc93235e6f8b2a"], "inferred": true}, {"symbol": "\\| H \\|", "meaning": "损失函数$\\mathcal{L}(\\mathbf{w})$的Hessian矩阵的范数。", "evidence_ids": ["550077dc6afc93235e6f8b2a"], "inferred": true}]};
it('binds the complete gradient and norm terms from the cached DeepCompress explanation',()=>{
 const result=interactiveFormulaSymbols(normBound.latex,normBound.symbols.map(item=>item.symbol));
 expect(result.unmatchedDefinitions).toEqual([]);
 expect(result.occurrences.filter(item=>item.definitionIndices.includes(3))).toHaveLength(2);
 expect(result.occurrences.filter(item=>item.definitionIndices.includes(2))).toHaveLength(1);
 expect(result.occurrences.filter(item=>item.latex===String.raw`\mathbf {w}`)).toHaveLength(0);
 expect(result.occurrences.every((item,index)=>!index||result.occurrences[index-1].end<=item.start)).toBe(true);
});
it.each([
 [String.raw`\left\|\delta w\right\|^2`,String.raw`\lVert\delta w\rVert`],
 [String.raw`\bigl\lVert x\bigr\rVert`,String.raw`\|x\|`],
 [String.raw`\lVert x\rVert_2`,String.raw`\|x\|_2`],
 [String.raw`\left|x\right|`,String.raw`\lvert x\rvert`],
])('matches equivalent delimiter notation while preserving the original source: %s',(latex,definition)=>{
 const result=interactiveFormulaSymbols(latex,[definition]);
 expect(result.unmatchedDefinitions).toEqual([]);
 const matched=result.occurrences.find(item=>item.definitionIndices.includes(0));
 expect(matched).toBeDefined();
 expect(latex.slice(matched!.start,matched!.end)).toBe(matched!.latex);
});
it('does not confuse absolute values with norms or match a norm inside a subscript',()=>{
 expect(interactiveFormulaSymbols('|w|',[String.raw`\|w\|`]).unmatchedDefinitions).toEqual([0]);
 expect(interactiveFormulaSymbols(String.raw`x_{\|w\|}`,[String.raw`\|w\|`]).unmatchedDefinitions).toEqual([0]);
});

it.each([
 [String.raw`\sqrt{d_k}`,String.raw`\sqrt{d_k}`],
 [String.raw`\sqrt [3] {x+y}`,String.raw`\sqrt[3]{x+y}`],
 [String.raw`\frac {QK^T} {\sqrt {d_k}}`,String.raw`\sqrt{d_k}`],
 [String.raw`\dfrac{x}{y}`,String.raw`\frac{x}{y}`],
 [String.raw`\sqrt{\frac{x}{y}}`,String.raw`\sqrt{\frac{x}{y}}`],
])('binds a complete structural expression: %s',(latex,definition)=>{
 const result=interactiveFormulaSymbols(latex,[definition]);
 expect(result.unmatchedDefinitions).toEqual([]);
 expect(result.occurrences.filter(o=>o.definitionIndices.includes(0))).toHaveLength(1);
 expect(result.occurrences.find(o=>o.definitionIndices.includes(0))?.latex).not.toBe('d_k');
});
it('preserves structural scope when normalizing expressions',()=>{
 expect(normalizeFormulaSymbol(String.raw`\sqrt{a+b}`)).not.toBe(normalizeFormulaSymbol(String.raw`\sqrt{a}+b`));
 expect(normalizeFormulaSymbol(String.raw`\frac{ab}{c}`)).not.toBe(normalizeFormulaSymbol(String.raw`\frac{a}{bc}`));
 expect(normalizeFormulaSymbol(String.raw`\sqrt[3]{x}`)).not.toBe(normalizeFormulaSymbol(String.raw`\sqrt{x}`));
});
it('matches repaired prime and spacing notation without changing the source range',()=>{
 const latex=String.raw`L(w') + \sqrt{d_k}\,Q`;
 const result=interactiveFormulaSymbols(latex,[String.raw`L(w^{\prime})`,String.raw`\sqrt{d_k} Q`]);
 expect(result.unmatchedDefinitions).toEqual([]);
 expect(result.occurrences.map(item=>item.latex)).toEqual(["L(w')",String.raw`\sqrt{d_k}\,Q`]);
 expect(normalizeFormulaSymbol("w'")).not.toBe(normalizeFormulaSymbol('w'));
});
it('retains inner definitions without splitting the complete explained root',()=>{
 const result=interactiveFormulaSymbols(String.raw`\sqrt{d_k}+d_k`,[String.raw`\sqrt{d_k}`,'d_k']);
 expect(result.occurrences.map(o=>o.latex)).toEqual([String.raw`\sqrt{d_k}`,'d_k']);
 expect(result.unmatchedDefinitions).toEqual([]);
});
it.each([
 [String.raw`\sqrt{a+b}`,String.raw`\sqrt{a}+b`],
 [String.raw`\frac{ab}{c}`,String.raw`\frac{a}{bc}`],
 [String.raw`\sqrt[3]{x}`,String.raw`\sqrt{x}`],
])('never binds a structurally different explanation: %s',(latex,definition)=>{
 expect(interactiveFormulaSymbols(latex,[definition]).unmatchedDefinitions).toEqual([0]);
});
it('binds every repeated root and retains duplicate definitions on each whole target',()=>{
 const result=interactiveFormulaSymbols(String.raw`\sqrt{d_k}+\sqrt {d _ {k}}`,[String.raw`\sqrt{d_k}`,String.raw`\sqrt {d _ {k}}`]);
 expect(result.occurrences).toHaveLength(2);
 expect(result.occurrences.map(item=>item.definitionIndices)).toEqual([[0,1],[0,1]]);
});
