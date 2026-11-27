import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useState } from 'react';
import { ConfigProvider } from 'antd';
import ResumePresentationControl from './ResumePresentationControl';
import { DEFAULT_RESUME_PRESENTATION, type ResumePresentation } from '../../utils/resumePresentation';

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));

describe('resume template and photo controls', () => {
  it('offers six layouts, supports arrow-key selection, and returns focus after closing', async () => {
    const changed = vi.fn();
    function Control() {
      const [value, setValue] = useState(DEFAULT_RESUME_PRESENTATION);
      return <ResumePresentationControl value={value} onChange={next => { setValue(next); changed(next); }} files={[]} disabled={false} onUploadPhoto={vi.fn()} />;
    }
    render(<ConfigProvider theme={{ token: { motion: false } }}><Control /></ConfigProvider>);
    const trigger = screen.getByRole('button', { name: /chat.resumePresentation.template:/ });
    fireEvent.click(trigger);
    const group = await screen.findByRole('radiogroup');
    const choices = within(group).getAllByRole('radio');
    expect(choices).toHaveLength(6);
    expect(choices[0]).toHaveAttribute('aria-checked', 'true');
    choices[0].focus();
    fireEvent.keyDown(group, { key: 'ArrowRight' });
    expect(changed).toHaveBeenLastCalledWith(expect.objectContaining({ template: 'modern' }));
    expect(choices[1]).toHaveAttribute('aria-checked', 'true');
    expect(choices[1]).toHaveFocus();
    fireEvent.click(screen.getByRole('button', { name: 'chat.resumePresentation.done' }));
    await waitFor(() => expect(trigger).toHaveFocus());
  });

  it('requires an explicit image selection and exposes full-photo and center-crop modes', () => {
    const changed = vi.fn();
    const upload = vi.fn();
    const files = [{ id: 'photo', name: 'portrait.png', input: { input_type: 'image' as const, uri: '/uploads/portrait.png', filename: 'portrait.png' } }];
    const props = { onChange: changed, files, disabled: false, onUploadPhoto: upload };
    const view = render(<ResumePresentationControl {...props} value={DEFAULT_RESUME_PRESENTATION} />);
    expect(screen.getByRole('combobox', { name: 'chat.resumePresentation.photo' })).toHaveValue('');
    expect(screen.queryByRole('combobox', { name: 'chat.resumePresentation.photoFit' })).toBeNull();
    fireEvent.change(screen.getByRole('combobox', { name: 'chat.resumePresentation.photo' }), { target: { value: '/uploads/portrait.png' } });
    const selected: ResumePresentation = { ...DEFAULT_RESUME_PRESENTATION, photoUri: '/uploads/portrait.png' };
    expect(changed).toHaveBeenLastCalledWith(selected);
    view.rerender(<ResumePresentationControl {...props} value={selected} />);
    fireEvent.change(screen.getByRole('combobox', { name: 'chat.resumePresentation.photoFit' }), { target: { value: 'cover' } });
    expect(changed).toHaveBeenLastCalledWith({ ...selected, photoFit: 'cover' });
    fireEvent.click(screen.getByRole('button', { name: 'chat.resumePresentation.uploadPhoto' }));
    expect(upload).toHaveBeenCalledTimes(1);
  });
});
