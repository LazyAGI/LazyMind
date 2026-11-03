import { createRef } from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { DatasetTypeEnum } from '@/api/generated/knowledge-client';
import { afterEach, describe, expect, it, vi } from 'vitest';
import UpdateModal, { type UpdateImperativeProps } from './index';
import { saveKnowledgeBaseCapabilities } from '@/modules/learning/api';
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('@/modules/knowledge/utils/request', () => ({ getKnowledgeOptions: vi.fn().mockResolvedValue({capabilities: [], algorithms: [{algo_id:"general_algo"}]}), KnowledgeBaseServiceApi: () => ({ datasetServiceAllDatasetTags: vi.fn().mockResolvedValue({ data: { tags: [] } }), datasetServiceListAlgos: vi.fn().mockResolvedValue({ data: { algos: [{ algo_id: 'general_algo' }] } }) }) }));
vi.mock('@/modules/learning/api', () => ({ getLearningCatalog: vi.fn().mockResolvedValue({ capabilities: [] }), getKnowledgeBaseCapabilities: vi.fn().mockResolvedValue([]), saveKnowledgeBaseCapabilities: vi.fn().mockResolvedValue({}) }));
afterEach(cleanup);
describe('knowledge base editing', () => {
 it('edits metadata without offering or submitting a scene change', async () => {
  const ref = createRef<UpdateImperativeProps>();
  const onUpdate = vi.fn().mockResolvedValue(undefined);
  render(<ConfigProvider theme={{ token: { motion: false } }}><UpdateModal ref={ref} onUpdate={onUpdate} embeddingReady /></ConfigProvider>);
  const dataset = { dataset_id: 'paper-kb', display_name: 'papers', type: DatasetTypeEnum.DatasetTypeText, scene: 'academic_papers', processing_level: 'parsed', tags: ['test'], algo: { algo_id: 'general_algo' } };
  await act(async () => ref.current!.onOpen(dataset));
  const name = await screen.findByLabelText(/knowledge.knowledgeBaseName/);
  expect(screen.queryByLabelText('场景')).not.toBeInTheDocument();
  expect(screen.queryByLabelText('使用场景')).not.toBeInTheDocument();
  fireEvent.change(name, { target: { value: 'papers_updated' } });
  fireEvent.click(screen.getByRole('button', { name: /OK/ }));
  await waitFor(() => expect(onUpdate).toHaveBeenCalledWith(expect.objectContaining({ dataset_id: 'paper-kb', display_name: 'papers_updated', processing_level: 'parsed' })));
  expect(onUpdate.mock.calls[0][0]).not.toHaveProperty('scenario_key');
  expect(saveKnowledgeBaseCapabilities).not.toHaveBeenCalled();
 });
});
