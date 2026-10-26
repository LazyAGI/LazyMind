import { documentAcademicStates } from '../api/paperWorkflows';
import { loadPersistedPaperLibrary } from './store';

// Core owns entry and attachment membership; recognition details enrich task controls.
export async function loadPaperLibrarySources(dataset: string) {
  const saved = await loadPersistedPaperLibrary(dataset);
  const active = new Set(saved.data.papers.flatMap(paper => paper.attachmentIds));
  const documents = [...new Set(saved.data.attachments.filter(item => active.has(item.id) && item.available !== false).map(item => item.contentRef).filter((id): id is string => !!id))];
  const states = [] as Awaited<ReturnType<typeof documentAcademicStates>>;
  for (let offset = 0; offset < documents.length; offset += 100) states.push(...await documentAcademicStates(dataset, documents.slice(offset, offset + 100)));
  return { ...saved, states, active: saved.active || states.some(item => ['pending', 'processing'].includes(item.state.status) || item.processing_status === 'parsing') };
}
