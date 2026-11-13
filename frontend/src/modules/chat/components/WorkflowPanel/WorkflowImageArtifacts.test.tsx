import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';
import { SlotRenderer } from './SlotComponents';

vi.mock('./FilePreviewDrawer', () => ({ FilePreviewDrawer: () => null }));

beforeEach(async () => {
  await i18n.changeLanguage('zh-CN');
  vi.stubGlobal('Image', class {
    onload?: () => void;
    set src(_: string) { queueMicrotask(() => this.onload?.()); }
  });
});
afterEach(() => vi.unstubAllGlobals());

const slot: SlotRevision = {
  slot_id: 'images', slot: 'images', revision: 1, selected: true,
  list_index: 0, created_at: '', content_type: 'image',
};

it('displays a published image using the Core image object', async () => {
  render(<SlotRenderer slot={{ ...slot, caption: 'published image',
    artifact_value: { url: 'https://images.example/published.png' } }} readOnly expectedType='image' />);
  expect(await screen.findByRole('img', { name: 'published image' })).toHaveAttribute('src', 'https://images.example/published.png');
  expect(screen.queryByText('处理中…')).not.toBeInTheDocument();
});

it.each([{}, { url: '' }, 'unusable image metadata'])('reports invalid published image data instead of waiting forever: %j', (value) => {
  render(<SlotRenderer slot={{ ...slot, artifact_value: value }} readOnly expectedType='image' />);
  expect(screen.getByText('图片加载失败')).toBeVisible();
  expect(screen.queryByText('处理中…')).not.toBeInTheDocument();
});

it('keeps the pending placeholder for an image that has not been published', () => {
  render(<SlotRenderer slot={slot} readOnly expectedType='image' />);
  expect(screen.getByText('处理中…')).toBeVisible();
  expect(screen.queryByText('图片加载失败')).not.toBeInTheDocument();
});
