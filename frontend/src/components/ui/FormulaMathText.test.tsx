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
