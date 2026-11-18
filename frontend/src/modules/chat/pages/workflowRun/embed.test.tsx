import { Suspense } from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import Login from '@/modules/signin/pages/login';
import WorkflowRunEmbedPage from './embed';

const fixture = vi.hoisted(() => ({ local: false, user: null as null | { token: string }, login: vi.fn() }));
vi.mock('@/components/auth', () => ({ AgentAppsAuth: { getUserInfo: () => fixture.user } }));
vi.mock('@/runtime/localSession', () => ({ isLocalSessionEnabled: () => fixture.local }));
vi.mock('@/modules/signin/utils/request', () => ({
  loginByPassword: fixture.login,
  unwrapLoginResponse: (value: unknown) => value,
  storeLoginSession: async () => { fixture.user = { token: 'test-token' }; },
}));
vi.mock('@/components/request', () => ({ localizeErrorCode: (code: string) => code }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('./index', () => ({ default: () => <div>Workflow loaded</div> }));

const runPath = '/workflow-runs/test-run/embed?hostOrigin=http%3A%2F%2F127.0.0.1%3A19091';
function Location() {
  const location = useLocation();
  return <output data-testid='location'>{location.pathname}{location.search}</output>;
}
function renderRoute(entry: string | { pathname: string; state: Record<string, unknown> } = runPath) {
  return render(<MemoryRouter initialEntries={[entry]}><Suspense fallback='Loading'>
    <Routes>
      <Route path='/workflow-runs/:sessionId/embed' element={<WorkflowRunEmbedPage />} />
      <Route path='/login' element={<Login />} />
      <Route path='/agent/chat' element={<div>Chat</div>} />
    </Routes>
    <Location />
  </Suspense></MemoryRouter>);
}
beforeEach(() => {
  fixture.local = false;
  fixture.user = null;
  fixture.login.mockReset().mockResolvedValue({ data: { access_token: 'test-token' } });
  localStorage.clear();
  sessionStorage.clear();
});
afterEach(cleanup);

it('signs in before loading the run and returns to its exact embedded route', async () => {
  renderRoute();
  fireEvent.change(await screen.findByPlaceholderText('auth.pleaseInputAccount'), { target: { value: 'test-user' } });
  fireEvent.change(screen.getByPlaceholderText('auth.pleaseInputPassword'), { target: { value: 'test-password' } });
  expect(screen.queryByText('Workflow loaded')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'auth.login' }));
  expect(await screen.findByText('Workflow loaded')).toBeVisible();
  expect(fixture.login).toHaveBeenCalledWith({ username: 'test-user', password: 'test-password' });
  expect(screen.getByTestId('location')).toHaveTextContent(runPath);
});

it('rejects an external post-login destination', async () => {
  fixture.user = { token: 'test-token' };
  renderRoute({ pathname: '/login', state: { workflowRunReturnTo: 'https://other.example/workflow-runs/run/embed' } });
  expect(await screen.findByText('Chat')).toBeVisible();
});

it('preserves automatic local-session initialization', async () => {
  fixture.local = true;
  renderRoute();
  expect(await screen.findByText('Workflow loaded')).toBeVisible();
  expect(fixture.login).not.toHaveBeenCalled();
});
