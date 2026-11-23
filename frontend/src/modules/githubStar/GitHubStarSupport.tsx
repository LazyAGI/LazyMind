import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Select, Space, Typography } from 'antd';
import { useTranslation } from 'react-i18next';
import { AgentAppsAuth, AUTH_LOGOUT_EVENT, AUTH_USER_CHANGE_EVENT } from '@/components/auth';
import { acceptDeferredGitHubStarIntent, cancelDeferredGitHubStarIntent, markGitHubStarSupported,
  readDeferredGitHubStarIntent, type DeferredGitHubStarStatus } from './api';
import { publishGitHubStarSupport, subscribeGitHubStarSupport } from './supportNotifications';

export default function GitHubStarSupport() {
  const { t } = useTranslation();
  const owner = AgentAppsAuth.getUserInfo()?.userId || '';
  const identity = AgentAppsAuth.getSessionIdentity();
  const [authVersion, setAuthVersion] = useState(0);
  const [support, setSupport] = useState<DeferredGitHubStarStatus | null>(null);
  const [selected, setSelected] = useState<string>();
  const [working, setWorking] = useState(false);
  const [notice, setNotice] = useState('');
  const generation = useRef(0), busy = useRef(false), alive = useRef(false), preferenceAttempted = useRef(false);
  const current = useRef({ owner, identity }); current.current = { owner, identity };
  const label = (key: string) => t(`githubStar.${key}`);

  function valid(version: number, user: string, session: string) {
    return alive.current && generation.current === version && current.current.owner === user
      && current.current.identity === session && AgentAppsAuth.getSessionIdentity() === session;
  }
  function syncPreference() {
    if (preferenceAttempted.current) return;
    preferenceAttempted.current = true;
    void markGitHubStarSupported().catch(() => {});
  }

  useEffect(() => {
    const changed = () => setAuthVersion(version => version + 1);
    window.addEventListener(AUTH_USER_CHANGE_EVENT, changed);
    window.addEventListener(AUTH_LOGOUT_EVENT, changed);
    return () => {
      window.removeEventListener(AUTH_USER_CHANGE_EVENT, changed);
      window.removeEventListener(AUTH_LOGOUT_EVENT, changed);
    };
  }, []);
  useEffect(() => {
    alive.current = true;
    generation.current++; busy.current = false; preferenceAttempted.current = false;
    setSupport(null); setNotice(''); setSelected(undefined); setWorking(false);
    const refresh = async () => {
      const version = ++generation.current;
      try {
        const result = await readDeferredGitHubStarIntent();
        if (!valid(version, owner, identity) || result.repository !== 'LazyAGI/LazyMind') return;
        setSupport(result);
        const accounts = [...new Map(result.accounts.map(account => [account.account_id, account])).values()];
        setSelected(accounts.length === 1 ? accounts[0].connection_id : undefined);
        if (result.intent) syncPreference();
      } catch { /* Unknown history cannot authorize a new support request. */ }
    };
    if (owner) void refresh();
    const unsubscribe = subscribeGitHubStarSupport(owner, () => {
      if (owner) { busy.current = false; setWorking(false); void refresh(); }
    });
    return () => { alive.current = false; generation.current++; unsubscribe(); };
  }, [owner, identity, authVersion]);

  async function confirm() {
    if (!support?.can_receive || support.intent || busy.current) return;
    const account = support.accounts.find(item => item.connection_id === selected);
    if (support.accounts.length && !account) return;
    const version = ++generation.current, user = owner, session = identity;
    busy.current = true; setWorking(true); setNotice('');
    try {
      const result = await acceptDeferredGitHubStarIntent(account ? { source: 'connection', confirmed: true,
        connection_id: account.connection_id, expected_account_id: account.account_id } : { source: 'local', confirmed: true });
      if (!valid(version, user, session)) return;
      if (result.repository !== 'LazyAGI/LazyMind' || result.accepted !== true || !result.intent?.intent_id) {
        setNotice('supportFailed'); return;
      }
      setSupport({ ...support, intent: result.intent }); setNotice('thankYou');
      syncPreference();
      busy.current = false; setWorking(false);
      publishGitHubStarSupport(user);
    } catch {
      if (valid(version, user, session)) setNotice('supportFailed');
    } finally {
      if (valid(version, user, session)) { busy.current = false; setWorking(false); }
    }
  }
  async function cancel() {
    const intent = support?.intent;
    if (!support || !intent || intent.status !== 'pending' || busy.current) return;
    const version = ++generation.current, user = owner, session = identity;
    busy.current = true; setWorking(true); setNotice('');
    try {
      const result = await cancelDeferredGitHubStarIntent(intent.intent_id);
      if (!valid(version, user, session)) return;
      if (result.repository !== 'LazyAGI/LazyMind' || result.intent?.intent_id !== intent.intent_id) {
        setNotice('supportFailed'); return;
      }
      setSupport({ ...support, intent: result.intent }); setNotice('supportCancelled');
    } catch {
      if (valid(version, user, session)) setNotice('supportFailed');
    } finally {
      if (valid(version, user, session)) { busy.current = false; setWorking(false); }
    }
  }

  if (!owner || !support || (!support.can_receive && !support.intent)) return null;
  const accounts = [...new Map(support.accounts.map(account => [account.account_id, account])).values()];
  const message = notice || (support.intent ? 'thankYou' : '');
  return <section aria-label={label('supportProject')} style={{ marginTop: 24 }}>
    <Typography.Title level={4}>{label('supportProject')}</Typography.Title>
    <Typography.Paragraph type='secondary'>LazyAGI/LazyMind</Typography.Paragraph>
    {!support.intent && <>
      <Typography.Paragraph>{label(accounts.length ? 'connectionSupportHint' : 'localSupportHint')}</Typography.Paragraph>
      {Boolean(accounts.length) && <Select aria-label={label('account')} placeholder={label('selectAccount')}
        value={selected} onChange={setSelected} disabled={working} style={{ width: '100%', marginBottom: 16 }}
        options={accounts.map(account => ({ value: account.connection_id, label: account.login }))} />}
      <Button type='primary' loading={working} disabled={Boolean(accounts.length && !selected)} onClick={() => void confirm()}>
        {label('supportProject')}
      </Button>
    </>}
    {message && (message === 'supportFailed' ? <Alert type='error' showIcon message={label(message)} />
      : <Typography.Paragraph role='status'>{label(message)}</Typography.Paragraph>)}
    {support.intent?.status === 'pending' && support.can_receive && <Space>
      <Button loading={working} onClick={() => void cancel()}>{label('cancelSupportRequest')}</Button>
    </Space>}
  </section>;
}
