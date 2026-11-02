import { mkdir, copyFile } from 'node:fs/promises';
import { embedPresentation } from '../../../workflow-agent-core/node/embed-presentation.mjs';
const destination = new URL('../../../../local/lazymind-cli/internal/workbuddyhost/assets/connector.mjs', import.meta.url);
await mkdir(new URL('.', destination), { recursive: true });
await copyFile(new URL('../dist/main.mjs', import.meta.url), destination);
const { startPresentationFrame } = await import('../dist/diagnostics/presentation-frame.mjs');
await embedPresentation(startPresentationFrame);
