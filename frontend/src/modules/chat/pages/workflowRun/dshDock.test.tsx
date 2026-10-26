import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, it, expect, vi } from 'vitest';
import type { ComponentType } from 'react';
import * as React from 'react';
import * as ReactDOM from 'react-dom';
import * as jsxRuntime from 'react/jsx-runtime';
import clientBundle from '../../../../../../integrations/dsh-workflow/lib/client.js?raw';

afterEach(() => vi.unstubAllGlobals());

it('scrolls at the transcript tail and retains the iframe through messages, expansion and restoration', () => {
  vi.stubGlobal('__LAZYMIND_WORKFLOW_PRESENTATION__', {
    serverName: 'lazymind', webUrl: 'http://localhost:8090',
  });
  const seats = new Map<string, ComponentType<any>>();
  const ctx = {
    effect: () => {},
    uiConversation: { events: { register: () => {} } },
    slots: {
      inject: (_name: string, register: () => void) => register(),
      register: ({ name }: { name: string }, component: ComponentType<any>) => { seats.set(name, component); },
    },
  };
  // Exercise the shipped module factory with the host's React runtime.
  new Function('window', clientBundle)(Object.assign(window, { __ModuleLoader__: {
    load({ factory }: { factory: (require: (name: string) => unknown) => { apply: (context: typeof ctx) => void } }) {
      factory(name => {
        if (name === 'react') return React;
        if (name === 'react-dom') return ReactDOM;
        if (name === 'react/jsx-runtime') return jsxRuntime;
        throw new Error(`Unexpected browser dependency: ${name}`);
      }).apply(ctx);
    },
  } }));
  Reflect.deleteProperty(window, '__ModuleLoader__');
  expect(seats.has('shell.overlay')).toBe(false);
  const Entry = seats.get('conversation.chat.node')!;
  const Dock = seats.get('conversation.input.dock')!;
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
  const frame = screen.getByTitle('LazyMind Workflow') as HTMLIFrameElement;
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
