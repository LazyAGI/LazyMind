import { act, fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import ArtifactCollectorCard from './index';
import { useTaskCenterStore, type ConversationArtifact } from '../../store/taskCenter';

const files: ConversationArtifact[] = [{
  artifact_id: 'files', conversation_id: 'conv', history_id: 'turn',
  producer_type: 'main_agent', slot: 'outputs', seq: 1, content_type: 'file_list',
  value: { paths: ['/static-files/one.pdf', '/static-files/two.pdf'] },
}];

beforeEach(() => {
  useTaskCenterStore.setState({
    artifactsByConversation: { conv: files }, loadConversationArtifacts: vi.fn(async () => {}),
  });
});

it('opens each file from its main card area without toggling the selection', () => {
  const preview = vi.fn();
  render(<ArtifactCollectorCard sessionId="conv" historyId="turn" onPreview={preview} />);
  const checks = screen.getAllByRole('checkbox');
  fireEvent.click(screen.getByRole('button', { name: 'two.pdf' }));
  expect(preview).toHaveBeenCalledWith(files[0], expect.objectContaining({ id: 'files:1', filename: 'two.pdf' }));
  expect(checks.every(check => (check as HTMLInputElement).checked)).toBe(true);
  fireEvent.click(checks[1]);
  expect(preview).toHaveBeenCalledTimes(1);
});

it('retains a manual selection when refreshed file metadata has the same identities', () => {
  const view = render(<ArtifactCollectorCard sessionId="conv" historyId="turn" />);
  fireEvent.click(screen.getAllByRole('checkbox')[1]);
  act(() => useTaskCenterStore.setState({ artifactsByConversation: { conv: files.map(file => ({ ...file })) } }));
  view.rerender(<ArtifactCollectorCard sessionId="conv" historyId="turn" />);
  expect(screen.getAllByRole('checkbox')[1]).not.toBeChecked();
});
