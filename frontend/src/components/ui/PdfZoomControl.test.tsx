import {fireEvent,render,screen} from '@testing-library/react';
import {describe,expect,it,vi} from 'vitest';
import {PdfZoomControl} from './PdfZoomControl';

describe('editable PDF zoom',()=>{
  it.each([['137',1.37],['999',2],['10',.5]])('applies %s on blur within supported bounds',(value,zoom)=>{
    const onChange=vi.fn();
    render(<PdfZoomControl zoom={1.25} disabled={false} onChange={onChange}/>);
    fireEvent.click(screen.getByRole('button',{name:'125%'}));
    expect(onChange).not.toHaveBeenCalled();
    const input=screen.getByRole('spinbutton');
    fireEvent.change(input,{target:{value}});
    fireEvent.blur(input);
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith(zoom);
  });
  it('ignores an empty draft and cancels with Escape without changing the zoom',()=>{
    const onChange=vi.fn();
    render(<PdfZoomControl zoom={1.25} disabled={false} onChange={onChange}/>);
    fireEvent.click(screen.getByRole('button',{name:'125%'}));
    fireEvent.change(screen.getByRole('spinbutton'),{target:{value:''}});
    fireEvent.blur(screen.getByRole('spinbutton'));
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button',{name:'125%'}));
    fireEvent.change(screen.getByRole('spinbutton'),{target:{value:'180'}});
    fireEvent.keyDown(screen.getByRole('spinbutton'),{key:'Escape'});
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole('button',{name:'125%'})).toBeInTheDocument();
  });
  it('drops an unfinished edit when the PDF is no longer loaded',()=>{
    const onChange=vi.fn();
    const view=render(<PdfZoomControl zoom={1.25} disabled={false} onChange={onChange}/>);
    fireEvent.click(screen.getByRole('button',{name:'125%'}));
    view.rerender(<PdfZoomControl zoom={1} disabled onChange={onChange}/>);
    expect(screen.queryByRole('spinbutton')).toBeNull();
    expect(screen.getByRole('button',{name:'100%'})).toBeDisabled();
    expect(onChange).not.toHaveBeenCalled();
  });
});
