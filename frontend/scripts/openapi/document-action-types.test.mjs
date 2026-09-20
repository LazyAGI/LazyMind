import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import ts from 'typescript';

// Compile real response examples against the actual generated client. Checking
// only existing UI usages cannot detect an incorrect but currently unused type.
test('execute response represents rewrite and numbering without unsafe fields', () => {
  const frontend = fileURLToPath(new URL('../../', import.meta.url));
  const probe = path.join(frontend, 'src/api/generated/core-client/response-contract-probe.ts');
  const source = `
import type { DocumentRewriteExecuteOpenAPIResponseData as Result } from './api';
const rewrite: Result = { artifact_id: 'revision', revision: 4, draft_version: 1 };
const numbering: Result = {
  artifact_id: 'revision', revision: 4, draft_version: 1,
  title: 'Title', representation: 'markdown', document: '# Title',
  numbering: { ordered_style: 'hierarchical', entries: {} }
};
declare const response: Result;
// @ts-expect-error Numbering fields require narrowing: rewrite omits them.
response.numbering.ordered_style;
if ('numbering' in response) {
  const style: string = response.numbering.ordered_style;
}
`;
  const options = { noEmit: true, strict: true, skipLibCheck: true, target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext, moduleResolution: ts.ModuleResolutionKind.Bundler };
  const host = ts.createCompilerHost(options);
  const readFile = host.readFile;
  host.readFile = file => path.resolve(file) === probe ? source : readFile(file);
  const program = ts.createProgram([probe], options, host);
  const diagnostics = ts.getPreEmitDiagnostics(program).filter(d => !d.file || path.resolve(d.file.fileName) === probe);
  assert.deepEqual(diagnostics.map(d => `${d.code}: ${ts.flattenDiagnosticMessageText(d.messageText, '\n')}`), []);
});

// Cross-reference operations have different required inputs; the generated
// client must not silently reuse rewrite's node-only IR selection contract.
test('cross-reference operation and selection contracts remain discriminated', () => {
  const frontend = fileURLToPath(new URL('../../', import.meta.url));
  const probe = path.join(frontend, 'src/api/generated/core-client/cross-reference-contract-probe.ts');
  const source = `
import type { DocumentCrossReferencePreviewRequest as Preview, DocumentCrossReferenceExecuteRequest as Execute, DocumentActionPreviewOpenAPIResponseData as Result } from './api';
const base = { action: 'cross_reference' as const, base_revision: 3 };
const list: Preview = { ...base, input: { operation: 'list_targets' } };
const add: Preview = { ...base, input: { operation: 'add', selection: { type: 'markdown', selected_text: 'target' }, target_id: 'first' } };
const retarget: Preview = { ...base, input: { operation: 'retarget', selection: { type: 'ir', node_id: 'p', selected_text: 'target' }, target_id: 'second' } };
const remove: Preview = { ...base, input: { operation: 'remove', selection: { type: 'ir', node_id: 'p', selected_text: 'target' } } };
const execute: Execute = { ...base, input: { commit_token: 'confirmed' } };
// @ts-expect-error IR selection must include selected_text.
const noText: Preview = { ...base, input: { operation: 'add', selection: { type: 'ir', node_id: 'p' }, target_id: 'first' } };
// @ts-expect-error Add must include a target.
const noTarget: Preview = { ...base, input: { operation: 'add', selection: { type: 'markdown', selected_text: 'target' } } };
// @ts-expect-error Remove must not carry a new target.
const removeTarget: Preview = { ...base, input: { operation: 'remove', selection: { type: 'markdown', selected_text: 'target' }, target_id: 'first' } };
// @ts-expect-error Listing must not carry a selection.
const listSelection: Preview = { ...base, input: { operation: 'list_targets', selection: { type: 'markdown', selected_text: 'target' } } };
const targets: Result = { representation: 'markdown', targets: [{ target_id: 'figure', type: 'image', title: '' }], invalid_references: [] };
const preview: Result = { representation: 'ir', operation: 'add', patch: { type: 'writer_ir_patch', payload: {} }, artifact: { content_type: 'json', value: {} }, commit: { token: 'confirmed' } };
declare const result: Result;
// @ts-expect-error Listing and conversion have no confirmation token.
result.commit.token;
if ('operation' in result) { const token: string = result.commit.token; }
`;
  const options = { noEmit: true, strict: true, skipLibCheck: true, target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext, moduleResolution: ts.ModuleResolutionKind.Bundler };
  const host = ts.createCompilerHost(options);
  const readFile = host.readFile;
  host.readFile = file => path.resolve(file) === probe ? source : readFile(file);
  const program = ts.createProgram([probe], options, host);
  const diagnostics = ts.getPreEmitDiagnostics(program).filter(d => !d.file || path.resolve(d.file.fileName) === probe);
  assert.deepEqual(diagnostics.map(d => `${d.code}: ${ts.flattenDiagnosticMessageText(d.messageText, '\n')}`), []);
});

test('provider discovery stays dynamic and does not imply credential policy', () => {
  const frontend = fileURLToPath(new URL('../../', import.meta.url));
  const probe = path.join(frontend, 'src/api/generated/core-client/provider-contract-probe.ts');
  const source = `
import type { DocumentProviderCatalog as Catalog, DocumentProvider } from './api';
const custom: Catalog = { providers: [{ id: 'custom.writer-v2', capabilities: ['future_capability'] }, { id: 'lark', capabilities: [] }] };
const empty: Catalog = { providers: [] };
// @ts-expect-error The providers array is required.
const absent: Catalog = {};
// @ts-expect-error Every entry requires its provider ID.
const noID: Catalog = { providers: [{ capabilities: [] }] };
// @ts-expect-error Capabilities must be an array even when empty.
const nullCaps: Catalog = { providers: [{ id: 'custom', capabilities: null }] };
declare const provider: DocumentProvider;
// @ts-expect-error Discovery does not expose or infer credential requirements.
provider.credential_required;
// @ts-expect-error Discovery does not imply the account is configured.
provider.account_connected;
`;
  const options = { noEmit: true, strict: true, skipLibCheck: true, target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext, moduleResolution: ts.ModuleResolutionKind.Bundler };
  const host = ts.createCompilerHost(options);
  const readFile = host.readFile;
  host.readFile = file => path.resolve(file) === probe ? source : readFile(file);
  const program = ts.createProgram([probe], options, host);
  const diagnostics = ts.getPreEmitDiagnostics(program).filter(d => !d.file || path.resolve(d.file.fileName) === probe);
  assert.deepEqual(diagnostics.map(d => `${d.code}: ${ts.flattenDiagnosticMessageText(d.messageText, '\n')}`), []);
});
