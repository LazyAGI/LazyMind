import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Modal, Select, Space, Typography } from 'antd';
import { GithubOutlined } from '@ant-design/icons';
import { useLocation, useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { AgentAppsAuth, AUTH_LOGOUT_EVENT, AUTH_USER_CHANGE_EVENT } from '@/components/auth';
import { isStarOpportunity, starReturnPath } from './intent';
import { GITHUB_STAR_OPPORTUNITY_EVENT, claimGitHubStarPrompt, findGitHubStarOpportunity, readGitHubStarAccounts,
  writeGitHubStar, markGitHubStarSupported, type GitHubStarAccount, type GitHubStarOpportunity } from './api';

export interface GitHubStarPromptProps {
  userId: string;
  blocked: boolean;
  returnTo: string;
  conversationId?: string;
}

type Invitation = { opportunity: GitHubStarOpportunity; accounts: GitHubStarAccount[]; connectionId?: string; scope: string };
type PendingInvitation = { opportunity: GitHubStarOpportunity; connectionId?: string; claimed?: boolean; userId: string; identity: string; scope: string };

function hasOpenDialog(excludeStar = false) {
  return Array.from(document.querySelectorAll<HTMLElement>('[role="dialog"], .artifact-preview')).some(dialog =>
    dialog.getClientRects().length > 0 && !(excludeStar && dialog.closest('.github-star-prompt')));
}

function isPageHidden() {
  return document.visibilityState === 'hidden';
}

export default function GitHubStarPrompt({ userId, blocked, returnTo, conversationId }: GitHubStarPromptProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const viewScope = conversationId || location.pathname + location.search;
  const [invitation, setInvitation] = useState<Invitation | null>(null);
  const [selected, setSelected] = useState<string>();
  const [phase, setPhase] = useState<'star' | 'connect' | 'retry' | 'success'>('star');
  const [notice, setNotice] = useState('');
  const [working, setWorking] = useState(false);
  const [pending, setPending] = useState<PendingInvitation | null>(null);
  const current = useRef({ userId, blocked, conversationId, viewScope }); current.current = { userId, blocked, conversationId, viewScope };
  const generation = useRef(0);
  const busy = useRef(false);
  const shown = useRef(new Set<string>());
  const processedResume = useRef<unknown>(null);
  const alive = useRef(true);
  const label = (key: string) => t(`githubStar.${key}`);

  function valid(owner: string, version: number, identity: string, scope: string) {
    return alive.current && current.current.userId === owner && generation.current === version
      && !current.current.blocked && current.current.viewScope === scope
      && AgentAppsAuth.getSessionIdentity() === identity;
  }
  function close() {
    generation.current++; busy.current = false; setPending(null); setInvitation(null); setWorking(false); setNotice('');
    if (location.state?.githubStarResume) navigate(location.pathname + location.search, { replace: true, state: null });
  }
  const load = useRef<(request: PendingInvitation) => Promise<void>>();
  load.current = async request => {
    const { opportunity, connectionId, userId: owner, identity, scope } = request;
    if (!userId || owner !== userId || scope !== viewScope || identity !== AgentAppsAuth.getSessionIdentity() || busy.current || (!connectionId && shown.current.has(userId))) return;
    const version = ++generation.current;
    busy.current = true;
    try {
      const result = await readGitHubStarAccounts();
      if (!valid(owner, version, identity, scope) || result.repository !== 'LazyAGI/LazyMind' || result.can_star === false) return;
      if (isPageHidden() || hasOpenDialog(Boolean(connectionId))) { setPending({ ...request }); return; }
      const latest = await findGitHubStarOpportunity(opportunity.conversation_id, undefined, [opportunity.task_id]);
      if (!valid(owner, version, identity, scope) || latest?.task_id !== opportunity.task_id) return;
      if (isPageHidden() || hasOpenDialog(Boolean(connectionId))) { setPending({ ...request }); return; }
      if (result.accounts.some(account => account.state === 'starred')) {
        if (connectionId) {
          setInvitation({ opportunity, accounts: [], connectionId, scope }); setPhase('success'); setNotice('alreadyStarred');
        }
        void markGitHubStarSupported().catch(() => {}); return;
      }
      const identities = new Map<string, GitHubStarAccount>();
      for (const account of result.accounts) {
        if (account.eligible && account.state === 'not_starred'
          && (!identities.has(account.account_id) || account.connection_id === connectionId)) identities.set(account.account_id, account);
      }
      const accounts = [...identities.values()];
      if (result.accounts.length && !accounts.length) {
        if (connectionId) { setInvitation({ opportunity, accounts: [], connectionId, scope }); setPhase('retry'); setNotice('failed'); }
        return;
      }
      if (!connectionId && !request.claimed) {
        const claim = await claimGitHubStarPrompt(opportunity);
        if (!valid(owner, version, identity, scope) || !claim.show || claim.task_id !== opportunity.task_id) return;
        request = { ...request, claimed: true };
        const afterClaim = await findGitHubStarOpportunity(opportunity.conversation_id, undefined, [opportunity.task_id]);
        if (!valid(owner, version, identity, scope) || afterClaim?.task_id !== opportunity.task_id) return;
      }
      if (!valid(owner, version, identity, scope)) return;
      if (isPageHidden() || hasOpenDialog(Boolean(connectionId))) { setPending({ ...request }); return; }
      shown.current.add(owner);
      setInvitation({ opportunity, accounts, connectionId, scope });
      setSelected(connectionId ? accounts.find(account => account.connection_id === connectionId)?.connection_id
        : accounts.length === 1 ? accounts[0].connection_id : undefined);
      setPhase('star'); setNotice(connectionId && !accounts.some(account => account.connection_id === connectionId) ? 'accountChanged' : '');
    } catch {
      if (connectionId && valid(owner, version, identity, scope)) {
        if (isPageHidden() || hasOpenDialog(true)) { setPending({ ...request }); return; }
        setInvitation({ opportunity, accounts: [], connectionId, scope }); setPhase('retry'); setNotice('failed');
      }
    } finally { if (version === generation.current) busy.current = false; }
  };

  useEffect(() => {
    alive.current = true;
    const received = (event: Event) => {
      const opportunity = (event as CustomEvent).detail;
      if (!isStarOpportunity(opportunity) || busy.current || shown.current.has(current.current.userId)) return;
      const scope = current.current.viewScope;
      if (current.current.conversationId !== undefined && opportunity.conversation_id !== current.current.conversationId) return;
      setPending({ opportunity, userId: current.current.userId, identity: AgentAppsAuth.getSessionIdentity(), scope });
    };
    const reset = () => { generation.current++; busy.current = false; setPending(null); setInvitation(null); setWorking(false); };
    window.addEventListener(GITHUB_STAR_OPPORTUNITY_EVENT, received);
    window.addEventListener(AUTH_LOGOUT_EVENT, reset);
    const initialIdentity = AgentAppsAuth.getSessionIdentity();
    const changed = () => { if (AgentAppsAuth.getSessionIdentity() !== initialIdentity) reset(); };
    window.addEventListener(AUTH_USER_CHANGE_EVENT, changed);
    return () => {
      alive.current = false; generation.current++;
      window.removeEventListener(GITHUB_STAR_OPPORTUNITY_EVENT, received);
      window.removeEventListener(AUTH_LOGOUT_EVENT, reset);
      window.removeEventListener(AUTH_USER_CHANGE_EVENT, changed);
    };
  }, [userId]);
  useEffect(() => {
    generation.current++; busy.current = false; setPending(null); setInvitation(null); setWorking(false);
  }, [userId, viewScope]);
  useEffect(() => {
    if (blocked) { generation.current++; busy.current = false; setPending(null); setInvitation(null); setWorking(false); }
  }, [blocked]);
  useEffect(() => {
    const resume = location.state?.githubStarResume;
    if (resume === processedResume.current || !resume || resume.userId !== userId
      || typeof resume.connectionId !== 'string' || !isStarOpportunity(resume.opportunity)) return;
    processedResume.current = resume;
    setPending({ opportunity: resume.opportunity, connectionId: resume.connectionId, userId, identity: AgentAppsAuth.getSessionIdentity(), scope: viewScope });
  }, [location.state, userId, viewScope]);
  useEffect(() => {
    if (!pending || pending.scope !== viewScope) return;
    let started = false;
    const resume = () => {
      if (started || blocked || isPageHidden() || hasOpenDialog(Boolean(pending.connectionId))) return;
      started = true;
      setPending(null);
      void load.current?.(pending);
    };
    // Preview/dialog state only delays a completed Work opportunity. Closing a
    // preview without a pending completion never creates an invitation.
    const observer = new MutationObserver(resume);
    observer.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ['class', 'style', 'hidden', 'aria-hidden'] });
    document.addEventListener('visibilitychange', resume);
    resume();
    return () => { started = true; observer.disconnect(); document.removeEventListener('visibilitychange', resume); };
  }, [pending, blocked, viewScope]);

  async function confirm() {
    if (!invitation || invitation.scope !== viewScope || busy.current || blocked) return;
    if (!invitation.accounts.length) { setPhase('connect'); return; }
    const account = invitation.accounts.find(item => item.connection_id === selected);
    if (!account) return;
    const version = generation.current, owner = userId, identity = AgentAppsAuth.getSessionIdentity(), scope = viewScope;
    busy.current = true; setWorking(true); setNotice('');
    try {
      const result = await writeGitHubStar({ connection_id: account.connection_id, expected_account_id: account.account_id, confirmed: true });
      if (!valid(owner, version, identity, scope)) return;
      if (result.state !== 'starred' || result.repository !== 'LazyAGI/LazyMind'
        || result.connection_id !== account.connection_id || result.account_id !== account.account_id) {
        setNotice('accountChanged'); return;
      }
      setPhase('success'); setNotice(result.write_performed ? 'success' : 'alreadyStarred');
      void markGitHubStarSupported().catch(() => {});
    } catch (error) {
      if (!valid(owner, version, identity, scope)) return;
      const response = (error as { response?: { status?: number; data?: { reason?: string } } }).response;
      setNotice(response?.data?.reason === 'result_unknown' ? 'unknownResult'
        : response?.data?.reason === 'rate_limited' || response?.status === 429 ? 'rateLimited'
        : response?.status === 403 ? 'permissionDenied' : response?.status === 401 ? 'reauthorize' : response?.status === 409 ? 'accountChanged' : 'failed');
    } finally {
      if (version === generation.current) { busy.current = false; setWorking(false); }
    }
  }

  function connect() {
    const path = starReturnPath(returnTo);
    if (!path || !invitation || invitation.scope !== viewScope) return;
    const opportunity = invitation.opportunity; close();
    navigate('/cloud-documents/github', { state: { githubStarReturn: { userId, returnTo: path, opportunity } } });
  }

  return <Modal rootClassName='github-star-prompt' open={Boolean(invitation && invitation.scope === viewScope)} title={<Space><GithubOutlined aria-hidden />{label('title')}</Space>}
    onCancel={close} destroyOnHidden footer={<Space wrap>
      <Button aria-label={label('close')} onClick={close}>{label('close')}</Button>
      {phase === 'star' && <Button type='primary' loading={working}
        disabled={Boolean(invitation?.accounts.length && !selected)} onClick={() => void confirm()}>{label('star')}</Button>}
      {phase === 'connect' && <Button type='primary' onClick={connect}>{label('continueAuth')}</Button>}
      {['permissionDenied', 'accountChanged', 'reauthorize'].includes(notice) && <Button onClick={connect}>{label('manageAccount')}</Button>}
      {phase === 'retry' && <Button type='primary' onClick={() => {
        if (invitation) setPending({ opportunity: invitation.opportunity, connectionId: invitation.connectionId, userId, identity: AgentAppsAuth.getSessionIdentity(), scope: viewScope });
      }}>{t('common.retry')}</Button>}
    </Space>}>
    <Typography.Paragraph>{label('message')}</Typography.Paragraph>
    <Typography.Paragraph type='secondary'>LazyAGI/LazyMind</Typography.Paragraph>
    {phase === 'connect' && <Typography.Paragraph>{label('localHint')}</Typography.Paragraph>}
    {phase === 'star' && Boolean(invitation?.accounts.length) && <Select style={{ width: '100%', marginBottom: 16 }}
      aria-label={label('account')} placeholder={label('selectAccount')} value={selected}
      disabled={working} onChange={setSelected} options={invitation?.accounts.map(account => ({ value: account.connection_id, label: account.login }))} />}
    {notice && (phase === 'success' ? <Typography.Paragraph role='status'>{label(notice)}</Typography.Paragraph>
      : <Alert type='error' showIcon role='alert' message={label(notice)} />)}
  </Modal>;
}
