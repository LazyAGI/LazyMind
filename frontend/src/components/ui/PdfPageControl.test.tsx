import {fireEvent,render,screen} from '@testing-library/react';
import {describe,expect,it,vi} from 'vitest';
import {PdfPageControl} from './PdfPageControl';

describe('compact PDF page control',()=>{
  it('waits for Enter before jumping to a multi-digit page',()=>{
    const onChange=vi.fn();
    render(<PdfPageControl page={2} total={115} disabled={false} onChange={onChange}/>);
    fireEvent.click(screen.getByRole('button',{name:/2.*115/}));
    const input=screen.getByRole('spinbutton');
    fireEvent.change(input,{target:{value:'37'}});
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.keyDown(input,{key:'Enter'});
    expect(onChange).toHaveBeenCalledOnce();
    expect(onChange).toHaveBeenCalledWith(37);
  });

  it('clamps out-of-range pages and cancels edits with Escape',()=>{
    const onChange=vi.fn();
    render(<PdfPageControl page={2} total={15} disabled={false} onChange={onChange}/>);
    fireEvent.click(screen.getByRole('button',{name:/2.*15/}));
    fireEvent.change(screen.getByRole('spinbutton'),{target:{value:'99'}});
    fireEvent.keyDown(screen.getByRole('spinbutton'),{key:'Escape'});
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button',{name:/2.*15/}));
    fireEvent.change(screen.getByRole('spinbutton'),{target:{value:'99'}});
    fireEvent.blur(screen.getByRole('spinbutton'));
    expect(onChange).toHaveBeenCalledWith(15);
  });

  it('shows the latest page while idle and does not open before loading',()=>{
    const onChange=vi.fn();
    const view=render(<PdfPageControl page={1} total={0} disabled onChange={onChange}/>);
    expect(screen.getByRole('button')).toBeDisabled();
    view.rerender(<PdfPageControl page={12} total={30} disabled={false} onChange={onChange}/>);
    expect(screen.getByRole('button',{name:/12.*30/})).toBeInTheDocument();
  });

  it('drops an unfinished edit when the PDF switches back to loading',()=>{
    const onChange=vi.fn();
    const view=render(<PdfPageControl page={3} total={15} disabled={false} onChange={onChange}/>);
    fireEvent.click(screen.getByRole('button',{name:/3.*15/}));
    view.rerender(<PdfPageControl page={1} total={0} disabled onChange={onChange}/>);
    expect(screen.queryByRole('spinbutton')).toBeNull();
    expect(screen.getByRole('button')).toBeDisabled();
    expect(onChange).not.toHaveBeenCalled();
  });
});
