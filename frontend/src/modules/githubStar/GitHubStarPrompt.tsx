import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Modal, Select, Space, Typography } from 'antd';
import { GithubOutlined, StarFilled } from '@ant-design/icons';
import { useLocation, useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { AgentAppsAuth, AUTH_LOGOUT_EVENT, AUTH_USER_CHANGE_EVENT } from '@/components/auth';
import { isStarOpportunity, starReturnPath } from './intent';
import { GITHUB_STAR_OPPORTUNITY_EVENT, claimGitHubStarPrompt, findGitHubStarOpportunity, readGitHubStarAccounts,
  writeGitHubStar, writeLocalGitHubStar, markGitHubStarSupported, readDeferredGitHubStarIntent, acceptDeferredGitHubStarIntent,
  type GitHubStarIdentity, type GitHubStarOpportunity } from './api';
import { hasConfirmedGitHubStarSupport, publishGitHubStarSupport, subscribeGitHubStarSupport } from './supportNotifications';
import './GitHubStarPrompt.scss';

export interface GitHubStarPromptProps {
  userId: string;
  blocked: boolean;
  returnTo: string;
  conversationId?: string;
  mode?: 'default' | 'plugin';
  opportunity?: GitHubStarOpportunity | null;
}

type Invitation = { opportunity: GitHubStarOpportunity; accounts: GitHubStarIdentity[]; connectionId?: string; scope: string; deferred?: boolean; canStar?: boolean; identity?: string };
type PendingInvitation = { opportunity: GitHubStarOpportunity; connectionId?: string; claimed?: boolean; userId: string; identity: string; scope: string };

const repositoryURL = 'https://github.com/LazyAGI/LazyMind';

function hasOpenDialog(excludeStar = false) {
  return Array.from(document.querySelectorAll<HTMLElement>('[role="dialog"], .artifact-preview:not([data-inline="true"])')).some(dialog =>
    dialog.getClientRects().length > 0 && !(excludeStar && dialog.closest('.github-star-prompt')));
}

function isPageHidden() {
  return document.visibilityState === 'hidden';
}

export default function GitHubStarPrompt({ userId, blocked, returnTo, conversationId, mode = 'default', opportunity }: GitHubStarPromptProps) {
  const { t } = useTranslation();
  const plugin = mode === 'plugin';
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
  const suppressed = useRef(false);
  const acknowledged = useRef(false);
  const preferenceAttempted = useRef(false);
  const label = (key: string) => t(`githubStar.${key}`);

  function valid(owner: string, version: number, identity: string, scope: string, confirmed = false) {
    return alive.current && current.current.userId === owner && generation.current === version
      && !current.current.blocked && current.current.viewScope === scope
      && (confirmed || !suppressed.current) && AgentAppsAuth.getSessionIdentity() === identity;
  }
  function syncSupportPreference(expectedIdentity?: string) {
    if (preferenceAttempted.current) return;
    if (expectedIdentity !== undefined && (current.current.userId !== userId
      || AgentAppsAuth.getSessionIdentity() !== expectedIdentity || AgentAppsAuth.getUserInfo()?.userId !== userId)) return;
    preferenceAttempted.current = true;
    void markGitHubStarSupported().catch(() => {});
  }
  function invalidateInvitation() {
    generation.current++; busy.current = false; setPending(null); setWorking(false);
    if (!acknowledged.current) { setInvitation(null); setNotice(''); }
  }
  function rememberSupport(expectedIdentity?: string) {
    suppressed.current = true; invalidateInvitation(); syncSupportPreference(expectedIdentity);
  }
  function close() {
    acknowledged.current = false;
    generation.current++; busy.current = false; setPending(null); setInvitation(null); setWorking(false); setNotice('');
    if (location.state?.githubStarResume) navigate(location.pathname + location.search, { replace: true, state: null });
  }
  const load = useRef<(request: PendingInvitation) => Promise<void>>();
  load.current = async request => {
    const { opportunity, connectionId, userId: owner, identity, scope } = request;
    if (!userId || owner !== userId || scope !== viewScope || identity !== AgentAppsAuth.getSessionIdentity() || busy.current || suppressed.current || (!connectionId && shown.current.has(userId))) return;
    if (hasConfirmedGitHubStarSupport(owner)) { rememberSupport(identity); return; }
    const version = ++generation.current;
    busy.current = true;
    let accountQueryFailed = false;
    const checkHistory = async () => {
      const support = await readDeferredGitHubStarIntent();
      if (!valid(owner, version, identity, scope) || support.repository !== 'LazyAGI/LazyMind') return null;
      if (support.intent) { rememberSupport(); return null; }
      return support;
    };
    try {
      const support = await checkHistory();
      if (!support || (!plugin && support.local_supported && !support.can_receive)) return;
      const deferred = !plugin && support.local_supported && support.can_receive;
      let canStar = true;
      let alreadyStarred = false;
      let accounts: GitHubStarIdentity[];
      if (deferred) {
        accounts = support.accounts;
        if (accounts.length) {
          try {
            const observed = await readGitHubStarAccounts();
            alreadyStarred = observed.repository === 'LazyAGI/LazyMind' && observed.accounts.some(account => account.state === 'starred');
          } catch { /* Offline account metadata remains a usable confirmation source. */ }
          if (!valid(owner, version, identity, scope)) return;
        }
      } else {
        const result = await readGitHubStarAccounts().catch(error => {
          if (plugin) return { repository: 'LazyAGI/LazyMind', accounts: [], can_star: true };
          accountQueryFailed = true; throw error;
        });
        if (!valid(owner, version, identity, scope) || result.repository !== 'LazyAGI/LazyMind' || (!plugin && result.can_star === false)) return;
        canStar = result.can_star !== false;
        alreadyStarred = result.accounts.some(account => account.state === 'starred');
        accounts = result.accounts.filter(account => account.eligible && account.state === 'not_starred');
      }
      if (isPageHidden() || hasOpenDialog(Boolean(connectionId))) { setPending({ ...request }); return; }
      const latest = await findGitHubStarOpportunity(opportunity.conversation_id, undefined, [opportunity.task_id]);
      if (!valid(owner, version, identity, scope) || latest?.task_id !== opportunity.task_id) return;
      if (isPageHidden() || hasOpenDialog(Boolean(connectionId))) { setPending({ ...request }); return; }
      if (alreadyStarred) {
        if (!await checkHistory()) return;
        if (isPageHidden() || hasOpenDialog(Boolean(connectionId))) { setPending({ ...request }); return; }
        if (connectionId) {
          setInvitation({ opportunity, accounts: [], connectionId, scope }); setPhase('success'); setNotice('alreadyStarred');
        }
        syncSupportPreference(); return;
      }
      const identities = new Map<string, GitHubStarIdentity>();
      for (const account of accounts) {
        if (!identities.has(account.account_id) || account.connection_id === connectionId) identities.set(account.account_id, account);
      }
      accounts = [...identities.values()];
      if (!connectionId && !request.claimed) {
        const claim = await claimGitHubStarPrompt(opportunity);
        if (!valid(owner, version, identity, scope) || !claim.show || claim.task_id !== opportunity.task_id) return;
        request = { ...request, claimed: true };
        const afterClaim = await findGitHubStarOpportunity(opportunity.conversation_id, undefined, [opportunity.task_id]);
        if (!valid(owner, version, identity, scope) || afterClaim?.task_id !== opportunity.task_id) return;
      }
      if (!valid(owner, version, identity, scope)) return;
      if (isPageHidden() || hasOpenDialog(Boolean(connectionId))) { setPending({ ...request }); return; }
      const finalSupport = await checkHistory();
      if (!finalSupport || (!plugin && (finalSupport.local_supported && finalSupport.can_receive) !== deferred)) return;
      if (isPageHidden() || hasOpenDialog(Boolean(connectionId))) { setPending({ ...request }); return; }
      shown.current.add(owner);
      setInvitation({ opportunity, accounts, connectionId, scope, deferred, canStar, identity });
      const connectedAccount = accounts.find(account => account.connection_id === connectionId);
      setSelected(connectionId ? connectedAccount?.connection_id
        : accounts.length === 1 ? accounts[0].connection_id : undefined);
      setPhase(connectionId && !connectedAccount ? 'retry' : 'star');
      setNotice(connectionId && !connectedAccount ? 'accountChanged' : '');
    } catch {
      if (accountQueryFailed && connectionId && valid(owner, version, identity, scope)) {
        if (isPageHidden() || hasOpenDialog(true)) { setPending({ ...request }); return; }
        try {
          if (!await checkHistory()) return;
          if (isPageHidden() || hasOpenDialog(true)) { setPending({ ...request }); return; }
          setInvitation({ opportunity, accounts: [], connectionId, scope }); setPhase('retry'); setNotice('failed');
        } catch { /* An unavailable support history never permits an invitation. */ }
      }
    } finally { if (version === generation.current) busy.current = false; }
  };

  useEffect(() => {
    alive.current = true;
    suppressed.current = false; acknowledged.current = false; preferenceAttempted.current = false;
    const initialIdentity = AgentAppsAuth.getSessionIdentity();
    const received = (event: Event) => {
      if (plugin) return;
      const opportunity = (event as CustomEvent).detail;
      if (!isStarOpportunity(opportunity) || busy.current || suppressed.current || shown.current.has(current.current.userId)) return;
      const scope = current.current.viewScope;
      if (current.current.conversationId !== undefined && opportunity.conversation_id !== current.current.conversationId) return;
      setPending({ opportunity, userId: current.current.userId, identity: AgentAppsAuth.getSessionIdentity(), scope });
    };
    const reset = () => {
      suppressed.current = false; acknowledged.current = false; preferenceAttempted.current = false;
      generation.current++; busy.current = false; setPending(null); setInvitation(null); setWorking(false);
    };
    const unsubscribeSupport = subscribeGitHubStarSupport(userId, () => {
      if (current.current.userId !== userId) return;
      const identity = AgentAppsAuth.getSessionIdentity();
      suppressed.current = true;
      if (plugin && acknowledged.current) return;
      invalidateInvitation();
      if (hasConfirmedGitHubStarSupport(userId)) { syncSupportPreference(initialIdentity); return; }
      const version = generation.current;
      void readDeferredGitHubStarIntent().then(support => {
        if (alive.current && current.current.userId === userId && generation.current === version
          && AgentAppsAuth.getSessionIdentity() === identity && support.repository === 'LazyAGI/LazyMind' && support.intent) syncSupportPreference();
      }).catch(() => {});
    });
    window.addEventListener(GITHUB_STAR_OPPORTUNITY_EVENT, received);
    window.addEventListener(AUTH_LOGOUT_EVENT, reset);
    const changed = () => { if (AgentAppsAuth.getSessionIdentity() !== initialIdentity) reset(); };
    window.addEventListener(AUTH_USER_CHANGE_EVENT, changed);
    return () => {
      alive.current = false; generation.current++;
      window.removeEventListener(GITHUB_STAR_OPPORTUNITY_EVENT, received);
      window.removeEventListener(AUTH_LOGOUT_EVENT, reset);
      window.removeEventListener(AUTH_USER_CHANGE_EVENT, changed);
      unsubscribeSupport();
    };
  }, [userId, mode]);
  useEffect(() => {
    generation.current++; busy.current = false; setPending(null); setInvitation(null); setWorking(false);
  }, [userId, viewScope, mode]);
  useEffect(() => {
    if (blocked) { generation.current++; busy.current = false; setPending(null); setInvitation(null); setWorking(false); }
  }, [blocked]);
  useEffect(() => {
    if (plugin) return;
    const resume = location.state?.githubStarResume;
    if (resume === processedResume.current || !resume || resume.userId !== userId
      || typeof resume.connectionId !== 'string' || !isStarOpportunity(resume.opportunity)) return;
    processedResume.current = resume;
    setPending({ opportunity: resume.opportunity, connectionId: resume.connectionId, userId, identity: AgentAppsAuth.getSessionIdentity(), scope: viewScope });
  }, [location.state, userId, viewScope, mode]);
  useEffect(() => {
    if (!plugin || !userId || !isStarOpportunity(opportunity) || suppressed.current || shown.current.has(userId)) return;
    if (conversationId !== undefined && opportunity.conversation_id !== conversationId) return;
    setPending({ opportunity, userId, identity: AgentAppsAuth.getSessionIdentity(), scope: viewScope });
  }, [opportunity, userId, viewScope, mode]);
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
    if (plugin && invitation.identity !== AgentAppsAuth.getSessionIdentity()) { close(); return; }
    const account = invitation.accounts.find(item => item.connection_id === selected);
    if (invitation.connectionId && !account) { setPhase('retry'); setNotice('accountChanged'); return; }
    if (invitation.accounts.length && !account) return;
    const version = generation.current, owner = userId, identity = AgentAppsAuth.getSessionIdentity(), scope = viewScope;
    busy.current = true; setWorking(true); setNotice('');
    let popup: Window | null = null, openedRepository = false;
    if (plugin) {
      // Keep the confirmed request alive while other invitations are suppressed.
      acknowledged.current = true; suppressed.current = true;
      publishGitHubStarSupport(owner, true); syncSupportPreference(identity);
      if (!account || invitation.canStar === false) {
        try { popup = window.open('about:blank', '_blank'); if (popup) popup.opener = null; } catch { /* The repository link remains available. */ }
      }
    }
    const stillCurrent = () => valid(owner, version, identity, scope, plugin);
    const openRepository = () => {
      setPhase('connect');
      try {
        if (popup && !popup.closed) { popup.location.replace(repositoryURL); openedRepository = true; }
        else window.open(repositoryURL, '_blank', 'noopener,noreferrer');
      } catch { /* Keep the manual repository link if the host blocks navigation. */ }
    };
    try {
      if (plugin && invitation.canStar === false) { openRepository(); return; }
      if (invitation.deferred) {
        const result = await acceptDeferredGitHubStarIntent(account ? { confirmed: true, source: 'connection',
          connection_id: account.connection_id, expected_account_id: account.account_id } : { confirmed: true, source: 'local' });
        if (!alive.current || current.current.userId !== owner || AgentAppsAuth.getSessionIdentity() !== identity) return;
        if (result.repository !== 'LazyAGI/LazyMind' || result.accepted !== true || !result.intent?.intent_id) {
          if (valid(owner, version, identity, scope)) setNotice('supportFailed');
          return;
        }
        const showThanks = valid(owner, version, identity, scope);
        acknowledged.current = showThanks;
        syncSupportPreference();
        publishGitHubStarSupport(owner);
        if (showThanks) {
          setInvitation(invitation); setPhase('success'); setNotice('thankYou'); setWorking(false); busy.current = false;
        }
        return;
      }
      const result = account
        ? await writeGitHubStar({ connection_id: account.connection_id, expected_account_id: account.account_id, confirmed: true })
        : await writeLocalGitHubStar({ confirmed: true });
      if (!stillCurrent()) return;
      if (!account && result.state === 'connection_required' && result.repository === 'LazyAGI/LazyMind') {
        if (plugin) openRepository(); else setPhase('connect'); return;
      }
      if (result.state !== 'starred' || result.repository !== 'LazyAGI/LazyMind'
        || (account && (!('connection_id' in result) || result.connection_id !== account.connection_id || result.account_id !== account.account_id))
        || (!account && (!result.account_id || !result.login))) {
        if (plugin) setPhase('connect');
        setNotice('accountChanged'); return;
      }
      setPhase('success'); setNotice(result.write_performed ? 'success' : 'alreadyStarred');
      if (!plugin) void markGitHubStarSupported().catch(() => {});
    } catch (error) {
      if (!stillCurrent()) return;
      if (plugin) setPhase('connect');
      if (invitation.deferred) { setNotice('supportFailed'); return; }
      const response = (error as { response?: { status?: number; data?: { reason?: string } } }).response;
      setNotice(response?.data?.reason === 'result_unknown' ? 'unknownResult'
        : response?.data?.reason === 'rate_limited' || response?.status === 429 ? 'rateLimited'
        : response?.status === 403 ? 'permissionDenied' : response?.status === 401 ? 'reauthorize' : response?.status === 409 ? 'accountChanged' : 'failed');
    } finally {
      if (popup && !openedRepository) popup.close();
      if (version === generation.current) { busy.current = false; setWorking(false); }
    }
  }

  function connect() {
    const path = starReturnPath(location.pathname + location.search) || starReturnPath(returnTo);
    if (!path || !invitation || invitation.scope !== viewScope) return;
    const opportunity = invitation.opportunity; close();
    navigate('/cloud-documents/github', { state: { githubStarReturn: { userId, returnTo: path, opportunity } } });
  }

  return <Modal rootClassName={`github-star-prompt${plugin ? ' github-star-prompt--plugin' : ''}`} width={plugin ? 480 : undefined}
    open={Boolean(invitation && invitation.scope === viewScope)} title={<Space><GithubOutlined aria-hidden />{label(plugin ? 'pluginTitle' : 'title')}</Space>}
    onCancel={close} destroyOnHidden footer={<Space wrap>
      <Button aria-label={label('close')} onClick={close}>{label('close')}</Button>
      {phase === 'star' && <Button type='primary' loading={working} icon={plugin ? <StarFilled aria-hidden /> : undefined}
        disabled={Boolean(invitation?.accounts.length && !selected)} onClick={() => void confirm()}>{label(plugin ? 'pluginStar' : 'star')}</Button>}
      {!plugin && phase === 'connect' && <Button type='primary' onClick={connect}>{label('continueAuth')}</Button>}
      {!plugin && ['permissionDenied', 'accountChanged', 'reauthorize'].includes(notice) && <Button onClick={connect}>{label('manageAccount')}</Button>}
      {phase === 'retry' && <Button type='primary' onClick={() => {
        if (invitation) setPending({ opportunity: invitation.opportunity, connectionId: invitation.connectionId, userId, identity: AgentAppsAuth.getSessionIdentity(), scope: viewScope });
      }}>{t('common.retry')}</Button>}
    </Space>}>
    <Typography.Paragraph>{label(plugin ? 'pluginMessage' : 'message')}</Typography.Paragraph>
    <Typography.Paragraph type='secondary'>LazyAGI/LazyMind</Typography.Paragraph>
    {phase === 'star' && invitation?.deferred && <Typography.Paragraph>{label(invitation.accounts.length ? 'connectionSupportHint' : 'localSupportHint')}</Typography.Paragraph>}
    {!plugin && phase === 'connect' && <Typography.Paragraph>{label('localHint')}</Typography.Paragraph>}
    {plugin && phase === 'connect' && <>
      <Typography.Paragraph>{label('thankYou')}</Typography.Paragraph>
      <Typography.Paragraph><a href={repositoryURL} target='_blank' rel='noopener noreferrer'>{label('pluginRepository')}</a></Typography.Paragraph>
    </>}
    {phase === 'star' && Boolean(invitation?.accounts.length) && <Select style={{ width: '100%', marginBottom: 16 }}
      aria-label={label('account')} placeholder={label('selectAccount')} value={selected}
      disabled={working} onChange={setSelected} options={invitation?.accounts.map(account => ({ value: account.connection_id, label: account.login }))} />}
    {notice && (phase === 'success' ? <Typography.Paragraph role='status'>{label(notice)}</Typography.Paragraph>
      : <Alert type='error' showIcon role='alert' message={label(notice)} />)}
  </Modal>;
}
