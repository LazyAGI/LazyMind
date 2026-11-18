import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { ConfigProvider } from 'antd';
import i18n from '@/i18n';
import { WeChatTemplateSelect } from './WeChatTemplateSelect';

const templates = [
  { id: 'clean', name: 'Clean Reading' },
  { id: 'editorial_contrast', name: 'Editorial Contrast' },
  { id: 'warm_humanist', name: 'Warm Humanist' },
];
const change = vi.fn();
beforeEach(async () => {
  change.mockClear();
  await i18n.changeLanguage('en-US');
});
function view(disabled = false) {
  return <ConfigProvider theme={{ token: { motion: false } }}>
    <WeChatTemplateSelect id='template' templates={templates} value='clean' onChange={change} disabled={disabled} />
  </ConfigProvider>;
}

async function openPreview(name = 'Warm Humanist') {
  fireEvent.mouseDown(screen.getByRole('combobox'));
  const option = await screen.findByRole('option', { name: new RegExp(name) });
  fireEvent.mouseEnter(option.querySelector('.wechat-template-option')!);
  const preview = await screen.findByRole('article', { name });
  return { option, preview };
}

it.each(templates)('previews $name on hover and dismisses on leave without selecting it', async template => {
  render(view());
  const { option, preview } = await openPreview(template.name);
  expect(preview).toHaveTextContent('让文字，也有呼吸感');
  expect(change).not.toHaveBeenCalled();
  fireEvent.mouseLeave(option.querySelector('.wechat-template-option')!);
  await waitFor(() => expect(preview).not.toBeInTheDocument());
});

it('selects the hovered template and closes its preview', async () => {
  render(view());
  const { option, preview } = await openPreview();
  fireEvent.click(option);
  expect(change).toHaveBeenCalledWith('warm_humanist', expect.anything());
  await waitFor(() => expect(preview).not.toBeInTheDocument());
});

it('keeps reading clicks and scrolling out of template selection', async () => {
  render(view());
  const { preview: article } = await openPreview();
  const preview = screen.getByRole('region', { name: 'Preview template style' });
  await waitFor(() => expect(preview).toBeVisible());
  for (const target of [preview.querySelector('header')!, article.querySelector('p')!]) {
    const pointerDown = new MouseEvent('mousedown', { bubbles: true, cancelable: true });
    fireEvent(target, pointerDown);
    fireEvent.click(target);
    expect(pointerDown.defaultPrevented).toBe(true);
    expect(screen.getByRole('combobox')).toHaveAttribute('aria-expanded', 'true');
    expect(preview).toBeVisible();
  }
  fireEvent.wheel(article, { deltaY: 240 });
  expect(article).toBeVisible();
  expect(change).not.toHaveBeenCalled();
});

it('dismisses the preview with Escape and clears it when disabled', async () => {
  const { rerender } = render(view());
  const combobox = screen.getByRole('combobox');
  const { preview } = await openPreview('Clean Reading');
  fireEvent.keyDown(combobox, { key: 'Escape', keyCode: 27, which: 27 });
  await waitFor(() => expect(preview).not.toBeInTheDocument());
  const { preview: reopened } = await openPreview('Clean Reading');
  rerender(view(true));
  await waitFor(() => expect(reopened).not.toBeInTheDocument());
  expect(change).not.toHaveBeenCalled();
});
