import {render,screen} from '@testing-library/react';
import {describe,it,expect} from 'vitest';
import {FormulaMathText} from './FormulaMathText';
describe('safe explanatory math',()=>{
 it('renders mixed dollar and parenthesized math while preserving prose',()=>{
 const view=render(<FormulaMathText text={'Given \\(x^2\\), then $y = \\alpha$ is the result.'}/>);
 expect(view.container.querySelectorAll('.katex')).toHaveLength(2);
 expect(view.container).toHaveTextContent('Given');
 expect(view.container).toHaveTextContent('is the result.');
 });
 it('renders a bare symbol and preserves unsupported commands as text',()=>{
 const view=render(<><FormulaMathText text={'\\mathbf{x}'} mathOnly/><FormulaMathText text={'Unsupported $\\notARealCommand$ remains.'}/></>);
 expect(view.container.querySelectorAll('.katex')).toHaveLength(1);
 expect(screen.getByText('$\\notARealCommand$')).toBeInTheDocument();
 });
 it('never interprets model HTML or enables unsafe KaTeX links',()=>{
 const view=render(<FormulaMathText text={'<img src=x onerror=alert(1)> $\\href{javascript:alert(1)}{click}$'}/>);
 expect(view.container.querySelector('img')).toBeNull();
 expect(view.container.querySelector('a')).toBeNull();
 expect(view.container).toHaveTextContent('<img src=x onerror=alert(1)>');
 });
});

it('renders bare indexed variables from older explanations without changing snake_case prose',()=>{
 const view=render(<FormulaMathText text={'维度d_ff，权重 W_1；保留 model_name。'}/>);
 expect(view.container.querySelectorAll('.katex')).toHaveLength(2);
 expect(view.container).toHaveTextContent('model_name');
});

it('maps legacy summary aliases back to their known canonical symbols',()=>{
 const symbols=[String.raw`\theta_ {m d} ^ {(1)}`,String.raw`\theta_ {a g} ^ {(0)}`,String.raw`\hat {\theta} ^ {(0)}`,String.raw`\alpha ^ {(1)}`];
 const view=render(<FormulaMathText text={'模型参数theta_md^{(1)}由theta_ag^{(0)}与hat_theta^{(0)}加权，权重为alpha^{(1)}；保留model_name、source_blocks、display_latex和pretheta_md^{(1)}suffix。'} knownSymbols={symbols}/>);
 expect(Array.from(view.container.querySelectorAll('annotation'),node=>node.textContent)).toEqual(symbols);
 expect(view.container).toHaveTextContent('model_name、source_blocks、display_latex和pretheta_md^{(1)}suffix');
});

it('matches the longest known expression before an inner indexed symbol',()=>{
 const root=String.raw`\sqrt{d_k}`;
 const view=render(<FormulaMathText text={'分别用sqrt(d_k)与√d_k缩放，普通model_name保持原样。'} knownSymbols={[String.raw`d_k`,root]}/>);
 expect(Array.from(view.container.querySelectorAll('annotation'),node=>node.textContent)).toEqual([root,root]);
 expect(view.container).toHaveTextContent('model_name');
});

it('keeps explicit delimiters authoritative and preserves invalid or unsafe input as inert content',()=>{
 const root=String.raw`\sqrt{d_k}`;
 const view=render(<FormulaMathText text={'显式 $\\sqrt{d_k}$；无效 broken^{；危险 $\\href{javascript:alert(1)}{click}$。'} knownSymbols={[root,'broken^{']}/>);
 expect(Array.from(view.container.querySelectorAll('annotation'),node=>node.textContent)).toEqual([root,String.raw`\href{javascript:alert(1)}{click}`]);
 expect(view.container).toHaveTextContent('broken^{');
 expect(view.container.querySelector('a')).toBeNull();
});
