import { describe, expect, it } from 'vitest';
import { selectedNativePresentation } from './PresentationExportAction';
import type { SlotRevision, TabDef, WorkflowSession, WorkflowTabAction } from '@/modules/chat/store/workflowPanel';
const action: WorkflowTabAction = { id: 'export', type: 'export', provider: 'html-presentation', inputs: { pages: 'html', notes: 'notes' } };
const tab = { slots: [{ id: 'pptx' }, { id: 'html' }, { id: 'notes' }] } as TabDef;
const page = { selected: true, slot: 'html', sort_order: 1, created_at: '2026-11-09T08:00:00Z' } as SlotRevision;
const file = { selected: true, slot: 'pptx', content_type: 'file', created_at: '2026-11-09T08:01:00Z', artifact_value: { filename: 'deck.pptx', path: '/deck.pptx' } } as SlotRevision;
const session = (slots: SlotRevision[]) => ({ slots } as WorkflowSession);
describe('native editable presentation export', () => {
 it('uses the current native file for the entire deck without HTML conversion', () => {
  expect(selectedNativePresentation(action, tab, session([page,file]), [1])).toBe(file);
 });
 it('accepts artifacts atomically published by the same attempt regardless of upload order', () => {
  const native = {...file, producer_attempt_id:'attempt'};
  expect(selectedNativePresentation(action, tab, session([{...page,producer_attempt_id:'attempt',created_at:'2026-11-09T08:02:00Z'},native]),[1])).toBe(native);
 });
 it('does not substitute a whole deck for a subset or an outdated native delivery', () => {
  expect(selectedNativePresentation(action, tab, session([page,{...page,sort_order:2},file]),[1])).toBeUndefined();
  expect(selectedNativePresentation(action, tab, session([{...page,created_at:'2026-11-09T08:02:00Z'},file]),[1])).toBeUndefined();
  expect(selectedNativePresentation(action, tab, session([{...page,draft_version:2},file]),[1])).toBeUndefined();
 });
 it('does not pick a template outside the tab or guess between multiple files', () => {
  expect(selectedNativePresentation(action, tab, session([page,{...file,slot:'template'}]),[1])).toBeUndefined();
  expect(selectedNativePresentation(action, tab, session([page,file,{...file}]),[1])).toBeUndefined();
 });
});
