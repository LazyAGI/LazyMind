import { act, fireEvent, render, screen } from '@testing-library/react';
import { it, expect } from 'vitest';
import type { ComponentType } from 'react';
import * as React from 'react';
import * as jsxRuntime from 'react/jsx-runtime';
import clientBundle from '../../../../../../integrations/dsh-workflow/lib/client.js?raw';

it('docks above input and retains the iframe through updates, expansion and restoration', () => {
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
  const view = render(<><Entry node={{ data, anchorSeq: 1 }} /><Dock session={{ sessionId: 'host-1' }} /></>);
  const frame = screen.getByTitle('LazyMind Workflow') as HTMLIFrameElement;
  const container = frame.parentElement!;
  const src = frame.src;
  expect(container.style.width).toBe('100%');
  expect(container.style.maxWidth).toBe('var(--dsh-chat-content-width, 920px)');
  expect(container.style.alignSelf).toBe('center');
  view.rerender(<><Entry node={{ data: { ...data, operation: 'state' }, anchorSeq: 2 }} /><Dock session={{ sessionId: 'host-1' }} /></>);
  expect(screen.getByTitle('LazyMind Workflow')).toBe(frame);
  expect(frame.src).toBe(src);
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
  expect(screen.getByTitle('LazyMind Workflow')).toBe(frame);
  fireEvent.keyDown(window, { key: 'Escape' });
  expect(container.style.position).not.toBe('fixed');
  expect(container.style.maxWidth).toBe('var(--dsh-chat-content-width, 920px)');
  expect(screen.getByTitle('LazyMind Workflow')).toBe(frame);
  message('http://localhost:8090', frame.contentWindow, 'run-1', 'lazymind.workflow.toggle-collapse');
  expect(container.style.height).toBe('66px');
  message('http://localhost:8090', frame.contentWindow, 'run-1', 'lazymind.workflow.toggle-collapse');
  expect(container.style.height).toBe('min(480px, 55dvh)');
  message('http://localhost:8090', frame.contentWindow);
  message('http://localhost:8090', frame.contentWindow);
  expect(container.style.position).not.toBe('fixed');
  expect(screen.getByTitle('LazyMind Workflow')).toBe(frame);
});
