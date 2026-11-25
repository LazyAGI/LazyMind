import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, it, expect, onTestFinished, vi } from 'vitest';
import type { ComponentType } from 'react';
import * as React from 'react';
import * as ReactDOM from 'react-dom';
import * as jsxRuntime from 'react/jsx-runtime';
import clientBundle from '../../../../../../integrations/dsh-workflow/lib/client.js?raw';

afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });

const presentation = { serverName: 'lazymind', webUrl: 'http://localhost:8090' };
const reply = (sessionId: string | null, binding: unknown) => ({ ok: true, value: { sessionId, binding, config: presentation } });

function loadCompiledDock(call: (channel: string, endpoint: string, payload: { sessionId: string | null; clientBuild: string }, signal: AbortSignal) => Promise<unknown>) {
  vi.stubGlobal('__LAZYMIND_WORKFLOW_PRESENTATION__', presentation);
  const seats = new Map<string, ComponentType<any>>();
  const disposers: Array<() => void> = [];
  onTestFinished(() => { for (const dispose of disposers.reverse()) dispose(); });
  const ctx = {
    effect: (effect: () => () => void) => { disposers.push(effect()); },
    get: (service: string) => service === 'connection' ? { rpc: { call } } : undefined,
    uiConversation: { events: { register: () => () => {} } },
    slots: {
      inject: (_name: string, register: () => void) => register(),
      register: ({ name }: { name: string }, component: ComponentType<any>) => { seats.set(name, component); },
    },
  };
  // Exercise the shipped module factory with the host's React runtime.
  new Function('window', clientBundle)(Object.assign(window, { __ModuleLoader__: {
    load({ factory }: { factory: (require: (name: string) => unknown) => { apply: (context: typeof ctx) => void; connectorBuild: string } }) {
      const plugin = factory(name => {
        if (name === 'react') return React;
        if (name === 'react-dom') return ReactDOM;
        if (name === 'react/jsx-runtime') return jsxRuntime;
        throw new Error(`Unexpected browser dependency: ${name}`);
      });
      expect(plugin.connectorBuild).toMatch(/^\d+\.\d+\.\d+\+[a-f0-9]{16}$/);
      plugin.apply(ctx);
    },
  } }));
  Reflect.deleteProperty(window, '__ModuleLoader__');
  expect(seats.has('shell.overlay')).toBe(false);
  return { Entry: seats.get('conversation.chat.node')!, Dock: seats.get('conversation.input.dock')! };
}

it('scrolls at the transcript tail and retains the iframe through messages, expansion and restoration', async () => {
  const { Entry, Dock } = loadCompiledDock(async (_channel, _endpoint, { sessionId }) => reply(sessionId, { status: 'bound', runId: 'run-1' }));
  const data = { runId: 'run-1', hostSessionId: 'host-1', url: 'http://localhost:8090/workflow-runs/run-1', operation: 'start' };
  function Conversation({ updated = false }: { updated?: boolean }) {
    return <div data-conversation-scroll>
      <div data-testid="messages">
        <Entry node={{ data, anchorSeq: 1 }} />
        {updated && <><p>New agent message</p><Entry node={{ data: { ...data, operation: 'state' }, anchorSeq: 2 }} /></>}
      </div>
      <div data-composer-seat style={{ position: 'sticky', bottom: 0 }}>
        <Dock session={{ sessionId: 'host-1' }} /><textarea aria-label="Message" />
      </div>
    </div>;
  }
  const view = render(<Conversation />);
  const frame = await screen.findByTitle('LazyMind Workflow') as HTMLIFrameElement;
  const container = frame.parentElement!;
  const src = frame.src;
  const tail = frame.closest('[data-lazymind-workflow-tail]')!;
  expect(tail.parentElement).toHaveAttribute('data-conversation-scroll');
  expect(tail.previousElementSibling).toBe(screen.getByTestId('messages'));
  expect(tail.nextElementSibling).toHaveAttribute('data-composer-seat');
  expect(frame.closest('[data-composer-seat]')).toBeNull();
  expect(container.style.width).toBe('100%');
  expect(container.style.maxWidth).toBe('var(--dsh-chat-content-width, 920px)');
  expect(container.style.alignSelf).toBe('center');
  view.rerender(<Conversation updated />);
  expect(screen.getByTitle('LazyMind Workflow')).toBe(frame);
  expect(frame.src).toBe(src);
  expect(tail.previousElementSibling).toContainElement(screen.getByText('New agent message'));
  expect(screen.getAllByTitle('LazyMind Workflow')).toHaveLength(1);
  const message = (origin: string, source: MessageEventSource | null, sessionId = 'run-1',
    type = 'lazymind.workflow.toggle-expand') => {
    act(() => window.dispatchEvent(new MessageEvent('message', { origin, source,
      data: { type, sessionId } })));
  };
  message('http://untrusted.test', frame.contentWindow);
  message('http://localhost:8090', window);
  message('http://localhost:8090', frame.contentWindow, 'other-run');
  expect(container.style.position).not.toBe('fixed');
  message('http://localhost:8090', frame.contentWindow);
  expect(container.style.position).toBe('fixed');
  expect(container.style.maxWidth).toBe('none');
  expect(container.parentElement!.style.height).not.toBe('');
  expect(screen.getByTitle('LazyMind Workflow')).toBe(frame);
  fireEvent.keyDown(window, { key: 'Escape' });
  expect(container.style.position).not.toBe('fixed');
  expect(container.style.maxWidth).toBe('var(--dsh-chat-content-width, 920px)');
  expect(screen.getByTitle('LazyMind Workflow')).toBe(frame);
  const scroller = tail.parentElement!;
  scroller.scrollTop = 1000;
  // Model layout in the shared scroller; jsdom does not calculate element boxes.
  container.getBoundingClientRect = () => ({
    bottom: 1200 - scroller.scrollTop + (container.style.height === '66px' ? 66 : 700),
  } as DOMRect);
  const originalBottom = container.getBoundingClientRect().bottom;
  message('http://localhost:8090', frame.contentWindow, 'run-1', 'lazymind.workflow.toggle-collapse');
  expect(container.style.height).toBe('66px');
  expect(container.getBoundingClientRect().bottom).toBe(originalBottom);
  message('http://localhost:8090', frame.contentWindow, 'run-1', 'lazymind.workflow.toggle-collapse');
  expect(container.style.height).toBe('min(900px, 75dvh)');
  expect(scroller.scrollTop).toBe(1000);
  expect(container.getBoundingClientRect().bottom).toBe(originalBottom);
  scroller.scrollTop = 500;
  view.rerender(<Conversation updated />);
  expect(scroller.scrollTop).toBe(500);
  message('http://localhost:8090', frame.contentWindow);
  message('http://localhost:8090', frame.contentWindow);
  expect(container.style.position).not.toBe('fixed');
  expect(screen.getByTitle('LazyMind Workflow')).toBe(frame);
  view.unmount();
  expect(tail.isConnected).toBe(false);
});


it.each(['Desktop', 'Web'])('uses current Core bindings in the DSH %s client and fences old navigation results', async () => {
  vi.useFakeTimers();
  const requests: Array<{ sessionId: string; signal: AbortSignal; respond(value: unknown): void }> = [];
  const { Entry, Dock } = loadCompiledDock((channel, endpoint, { sessionId, clientBuild }, signal) => {
    expect(clientBuild).toMatch(/^\d+\.\d+\.\d+\+[a-f0-9]{16}$/);
    if (sessionId === null) return Promise.resolve(reply(null, { status: 'unbound' }));
    expect(channel).toBe('/api');
    expect(endpoint).toBe('lazymind/workflow-binding');
    return new Promise(resolve => requests.push({ sessionId, signal, respond: binding => resolve(reply(sessionId, binding)) }));
  });
  const historical = { runId: 'run-a', hostSessionId: 'A', url: 'http://localhost:8090/workflow-runs/run-a', operation: 'state' };
  function Chat({ id }: { id: string }) {
    return <div data-conversation-scroll>
      {id === 'B' && <Entry node={{ data: historical, anchorSeq: 1 }} sessionId="B" />}
      <div data-composer-seat><Dock session={{ sessionId: id }} /></div>
    </div>;
  }
  const view = render(<Chat id="A" />);
  const respond = (index: number, binding: unknown) => act(async () => { requests[index].respond(binding); });
  const match = (run: string) => expect((screen.getByTitle('LazyMind Workflow') as HTMLIFrameElement).src).toContain('/workflow-runs/' + run + '/embed');
  const absent = () => expect(screen.queryByTitle('LazyMind Workflow')).toBeNull();
  await respond(0, { status: 'bound', runId: 'run-a' });
  match('run-a');
  view.rerender(<Chat id="B" />);
  absent();
  expect(screen.getByRole('button', { name: 'Open workflow' })).toBeVisible();
  view.rerender(<Chat id="A" />);
  expect(requests[1].signal.aborted).toBe(true);
  await respond(1, { status: 'bound', runId: 'late-b' });
  absent();
  await respond(2, { status: 'bound', runId: 'return-a' });
  match('return-a');
  view.rerender(<Chat id="B" />);
  await respond(3, { status: 'unbound' });
  absent();
  await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
  await respond(4, { status: 'bound', runId: 'run-b' });
  match('run-b');
  await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
  await respond(5, { status: 'unavailable' });
  match('run-b');
  await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
  await respond(6, { status: 'access-denied' });
  absent();
  await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
  await respond(7, { status: 'bound', runId: 'run-b' });
  match('run-b');
  fireEvent.click(screen.getByRole('button', { name: 'Open workflow' }));
  match('run-a');
});
