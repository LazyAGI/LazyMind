import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { WriterHeadingNumberingMenu } from './WriterHeadingNumberingMenu';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const defaultProps = {
  x: 100,
  y: 100,
  targetId: 'section-1',
  mode: 'ordered' as const,
  orderedStyle: 'hierarchical' as const,
  restart: false,
};

describe('WriterHeadingNumberingMenu', () => {
  it('applies style, sequence and mode choices through the existing numbering contract', () => {
    const onApply = vi.fn();
    const onClose = vi.fn();
    const { rerender } = render(
      <WriterHeadingNumberingMenu {...defaultProps} onApply={onApply} onClose={onClose} />,
    );

    expect(screen.getByRole('radio', { name: 'chat.writerIR.hierarchicalStyle' })).toBeChecked();
    fireEvent.click(screen.getByRole('radio', { name: 'chat.writerIR.chineseStyle' }));
    expect(onApply).toHaveBeenLastCalledWith({ type: 'ordered_style', ordered_style: 'chinese' });

    rerender(<WriterHeadingNumberingMenu
      {...defaultProps} orderedStyle='chinese' onApply={onApply} onClose={onClose}
    />);
    expect(screen.getByRole('radio', { name: 'chat.writerIR.chineseStyle' })).toBeChecked();
    fireEvent.click(screen.getByText('chat.writerIR.restartNumbering'));
    expect(onApply).toHaveBeenLastCalledWith({ type: 'heading', target_id: 'section-1', restart: true });
    fireEvent.click(screen.getByText('chat.writerIR.unorderedHeading'));
    expect(onApply).toHaveBeenLastCalledWith({ type: 'heading', target_id: 'section-1', mode: 'unordered' });
    expect(onClose).not.toHaveBeenCalled();

    rerender(<WriterHeadingNumberingMenu
      {...defaultProps} mode='unordered' onApply={onApply} onClose={onClose}
    />);
    expect(screen.queryByRole('group', { name: 'chat.writerIR.numberingStyle' })).not.toBeInTheDocument();
    expect(screen.queryByText('chat.writerIR.restartNumbering')).not.toBeInTheDocument();
  });

  it('keeps disabled choices unavailable and preserves dismissal controls', () => {
    const onApply = vi.fn();
    const onClose = vi.fn();
    render(<WriterHeadingNumberingMenu
      {...defaultProps} disabled onApply={onApply} onClose={onClose}
    />);
    screen.getAllByRole('radio').forEach((radio) => expect(radio).toBeDisabled());
    fireEvent.click(screen.getByText('chat.writerIR.chineseStyle'));
    expect(onApply).not.toHaveBeenCalled();
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
    fireEvent.mouseDown(document.body);
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it('fits the measured menu inside the viewport and repositions when its mode changes', () => {
    vi.stubGlobal('innerWidth', 320);
    vi.stubGlobal('innerHeight', 300);
    const measure = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect')
      .mockReturnValue(new DOMRect(0, 0, 296, 224));
    const props = { ...defaultProps, x: 314, y: 290, onApply: vi.fn(), onClose: vi.fn() };
    const { rerender } = render(<WriterHeadingNumberingMenu {...props} />);
    const menu = screen.getByRole('dialog');
    expect(menu).toHaveStyle({ left: '16px', top: '68px' });

    measure.mockReturnValue(new DOMRect(0, 0, 296, 58));
    rerender(<WriterHeadingNumberingMenu {...props} mode='unordered' />);
    expect(menu).toHaveStyle({ left: '16px', top: '234px' });
  });
});
