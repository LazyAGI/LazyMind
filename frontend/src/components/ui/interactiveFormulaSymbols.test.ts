import {describe,it,expect} from 'vitest';
import {interactiveFormulaSymbols} from './interactiveFormulaSymbols';
const sps=String.raw`\mathcal {L} (I _ {i}, T _ {i}) = \mathcal {H} (y _ {i}, \mathcal {P} _ {i} ^ {i 2 t}) + \mathcal {H} (y _ {i}, \mathcal {P} _ {i} ^ {t 2 i}),\tag{1}`;
const cc=String.raw`\begin{array}{c} \mathcal {L} _ {c c} = \sum_ {i = 1} ^ {l} \operatorname{ReLU} \big (C r o s s A t t n (I, T _ {N P _ {i}}) \\ - C r o s s A t t n (I, N P _ {i}) - \epsilon \big) \end{array}`;
describe('deterministic complete formula symbol matching',()=>{
 it('matches the real SPS spacing/scripts and anchors full function definitions to their observed heads',()=>{
 const result=interactiveFormulaSymbols(sps,[String.raw`\mathcal{L}(I_i,T_i)`,String.raw`\mathcal{H}`,String.raw`y_i`,String.raw`\mathcal{P}_{i}^{i 2 t}`,String.raw`\tau`]);
 expect(result.occurrences.filter(o=>o.definitionIndices.includes(0)).map(o=>o.latex)).toEqual([String.raw`\mathcal {L}`]);
 expect(result.occurrences.filter(o=>o.definitionIndices.includes(1))).toHaveLength(2);
 expect(result.occurrences.filter(o=>o.definitionIndices.includes(2))).toHaveLength(2);
 expect(result.occurrences.find(o=>o.definitionIndices.includes(3))?.latex).toBe(String.raw`\mathcal {P} _ {i} ^ {i 2 t}`);
 expect(result.unmatchedDefinitions).toEqual([4]);
 expect(result.occurrences.find(o=>o.latex==='I _ {i}')?.definitionIndices).toEqual([]);
 });
 it('matches both real CC CrossAttn calls separately and preserves nested subscript atoms',()=>{
 const result=interactiveFormulaSymbols(cc,[String.raw`\mathcal{L}_{cc}`,'i',String.raw`\operatorname{ReLU}`,'CrossAttn(I, T_{NP_{i}})','CrossAttn(I, NP_{i})',String.raw`\epsilon`,'l']);
 expect(result.unmatchedDefinitions).toEqual([]);
 expect(result.occurrences.filter(o=>o.latex==='C r o s s A t t n').map(o=>o.definitionIndices)).toEqual([[3],[4]]);
 expect(result.occurrences.some(o=>o.latex==='T _ {N P _ {i}}')).toBe(true);
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
it('does not let a compound exponent definition swallow its inner click targets',()=>{
 const latex=String.raw`e^{\frac{x_i}{\tau}}+r_n^{q-}+\mathcal{P}_i^{i2t}`;
 const result=interactiveFormulaSymbols(latex,[String.raw`e^{\frac{x_i}{\tau}}`,String.raw`\tau`,String.raw`x_i`]);
 expect(result.unmatchedDefinitions).toEqual([0]);
 expect(result.occurrences.map(item=>item.latex)).toEqual(['e','x_i',String.raw`\tau`,'r_n^{q-}',String.raw`\mathcal{P}_i^{i2t}`]);
});
