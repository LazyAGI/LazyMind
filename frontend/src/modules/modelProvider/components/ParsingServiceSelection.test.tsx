import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
const api = vi.hoisted(() => ({ groups: vi.fn(), selected: vi.fn(), save: vi.fn() }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('../api', () => ({ modelProvidersApi: {
 apiCoreModelProvidersProviderGroupsGet: api.groups,
 apiCoreModelProvidersSelectedProvidersGet: api.selected,
 apiCoreModelProvidersSelectedProvidersPut: api.save,
}, unwrapModelProviderData: (data: unknown) => data }));
vi.mock('@/components/request', () => ({ getLocalizedErrorMessage: () => 'save failed' }));
import ParsingServiceSelection from './ParsingServiceSelection';
beforeEach(() => {
 vi.clearAllMocks();
 api.groups.mockResolvedValue({ data: { groups: [{ group_id: 'm', provider_name: 'MinerU', group_name: 'MinerU' }, { group_id: 'p', provider_name: 'PaddleOCR', group_name: 'PaddleOCR' }] } });
 api.selected.mockResolvedValue({ data: { selections: [{ category: 'ocr', group_id: 'm' }] } });
 api.save.mockResolvedValue({});
});
afterEach(cleanup);
it('switches exactly one active OCR service and restores it on reload', async () => {
 const changed = vi.fn();
 const view = render(<ParsingServiceSelection revision={0} onChanged={changed} />);
 expect(await screen.findByRole('radio', { name: 'MinerU' })).toBeChecked();
 fireEvent.click(screen.getByRole('radio', { name: 'PaddleOCR' }));
 await waitFor(() => expect(screen.getByRole('radio', { name: 'PaddleOCR' })).toBeChecked());
 expect(screen.getByRole('radio', { name: 'MinerU' })).not.toBeChecked();
 expect(api.save).toHaveBeenCalledWith({ setSelectedProviderOpenAPIRequest: { selections: [{ category: 'ocr', group_id: 'p' }] } });
 expect(changed).toHaveBeenCalledOnce();
 api.selected.mockResolvedValue({ data: { selections: [{ category: 'ocr', group_id: 'p' }] } });
 view.unmount();render(<ParsingServiceSelection revision={0} />);
 expect(await screen.findByRole('radio', { name: 'PaddleOCR' })).toBeChecked();
});
it('keeps the prior selection when saving fails', async () => {
 api.save.mockRejectedValue(new Error('offline'));
 render(<ParsingServiceSelection revision={0} />);
 fireEvent.click(await screen.findByRole('radio', { name: 'PaddleOCR' }));
 await screen.findByText('save failed');
 expect(screen.getByRole('radio', { name: 'MinerU' })).toBeChecked();
 expect(screen.getByRole('radio', { name: 'PaddleOCR' })).not.toBeChecked();
});
it('does not silently choose an available service when no selection exists', async () => {
 api.selected.mockResolvedValue({ data: { selections: [] } });
 render(<ParsingServiceSelection revision={0} />);
 expect(await screen.findByRole('radio', { name: 'MinerU' })).not.toBeChecked();
 expect(api.save).not.toHaveBeenCalled();
});
