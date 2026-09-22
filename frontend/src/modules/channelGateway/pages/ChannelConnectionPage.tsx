import { getLocalizedErrorMessage } from '@/components/request';
import { useCallback, useEffect, useRef, useState, type ChangeEvent } from 'react';
import {
  Alert,
  Button,
  Empty,
  Input,
  message,
  Modal,
  QRCode,
  Space,
  Spin,
  Tag,
  Typography,
} from 'antd';
import {
  CheckCircleFilled,
  CloseCircleFilled,
  LinkOutlined,
  LockOutlined,
  MobileOutlined,
  QrcodeOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
} from '@ant-design/icons';
import dayjs from 'dayjs';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import { getAccountDetail, getReferences, setDefaultRecipient, notificationError, providers, type AccountDetail, type Reference } from '@/modules/notifications/api';
import TargetPicker from '@/modules/notifications/TargetPicker';
import ChannelBrand from '@/modules/notifications/ChannelBrand';
import '@/modules/notifications/index.scss';

import type {
  ChannelAccount,
  ChannelProvider,
  ConnectionSession,
} from '../api';
import {
  archiveChannelAccount,
  channelAccountLabel,
  disconnectChannelAccount,
  isChannelAccountAvailable,
  isChannelAccountPendingActivation,
  pauseChannelAccount,
  resumeChannelAccount,
  listChannelAccounts,
} from '../api';
import { useChannelConnection } from '../hooks/useChannelConnection';
import './channelConnectionPage.scss';

const { Paragraph, Text, Title } = Typography;

function formatTime(value: string | null | undefined): string {
  if (!value) {
    return '-';
  }
  const parsed = dayjs(value);
  return parsed.isValid() ? parsed.format('YYYY-MM-DD HH:mm:ss') : value;
}

function statusColor(status: string): string {
  switch (status) {
    case 'connected':
    case 'running':
      return 'success';
    case 'waiting_scan':
    case 'scanned':
    case 'confirming':
    case 'preparing':
    case 'starting':
      return 'processing';
    case 'verification_required':
    case 'degraded':
      return 'warning';
    case 'failed':
    case 'expired':
    case 'canceled':
    case 'stopped':
    case 'unsupported':
      return 'error';
    default:
      return 'default';
  }
}

function canAct(
  session: ConnectionSession | null,
  action: ConnectionSession['allowed_actions'][number],
): boolean {
  return Boolean(session?.allowed_actions?.includes(action));
}

function isActiveScan(session: ConnectionSession | null): boolean {
  if (!session) {
    return false;
  }
  return !['connected', 'expired', 'canceled', 'failed'].includes(session.status);
}

function currentStep(session: ConnectionSession | null): number {
  if (!session) return 1;
  if (session.status === 'connected') return 3;
  if (['scanned', 'verification_required', 'confirming'].includes(session.status)) return 3;
  return 2;
}

function renderSessionVisual(
  session: ConnectionSession,
  labels: { preparing: string; connected: string; failed: string },
) {
  if (session.status === 'connected') {
    return (
      <div className="wechat-connection-result is-success" aria-label={labels.connected}>
        <CheckCircleFilled />
        <span>{labels.connected}</span>
      </div>
    );
  }
  if (['failed', 'expired', 'canceled'].includes(session.status)) {
    return (
      <div className="wechat-connection-result is-error" aria-label={labels.failed}>
        <CloseCircleFilled />
        <span>{labels.failed}</span>
      </div>
    );
  }
  if (session.qr?.payload) {
    return <QRCode value={session.qr.payload} size={220} status="active" bordered={false} />;
  }
  return (
    <div className="wechat-connection-qr-placeholder">
      <Spin />
      <span>{labels.preparing}</span>
    </div>
  );
}

interface ChannelConnectionPageProps {
  provider: ChannelProvider;
  accountId?: string;
  createNew?: boolean;
  autoStart?: boolean;
  onConnected?: (account?: ChannelAccount) => void;
}

function ChannelConnectionPage({ provider, accountId, createNew, autoStart, onConnected }: ChannelConnectionPageProps) {
  const translationKey = `channelGateway.${provider}`;
  const copy = (name: string) => {
    if (provider === 'feishu' && accountId) {
      if (['newConnectionTitle', 'guideTitle', 'readyTitle', 'stepConfirmTitle', 'sessionStatusMap.confirming'].includes(name)) return 'notifications.reauthorize';
      if (['newConnectionHint', 'guideHint', 'stepConfirmHint'].includes(name)) return 'notifications.reauthorizeHint';
      if (name === 'addAnotherAccount') return `${translationKey}.closePanel`;
    }
    return `${translationKey}.${name}`;
  };
  const channelIcon = <ChannelBrand channel={provider} />;
  const {
    t,
    accounts,
    session,
    sessionStarting,
    actionLoading,
    challengeValue,
    setChallengeValue,
    startScan,
    cancelScan,
    refreshQr,
    submitChallenge,
    closeSessionPanel,
  } = useChannelConnection(provider);

  useEffect(() => { if (session?.status === 'connected') onConnected?.(session.account || undefined); }, [session?.status, session?.account, onConnected]);
  const step = currentStep(session);
  const hasAccounts = accounts.length > 0;
  const activeScan = isActiveScan(session);
  const connectWorkspaceId = `${provider}-connect-workspace`;
  const connectTitleId = `${provider}-connect-title`;
  const autoStartedAccountId = useRef<string>();

  const beginScan = useCallback(() => startScan({ accountId, ...(provider === 'feishu' && accountId ? { reauthorize: true } : {}), ...(createNew ? { createNew: true } : {}) }), [accountId, createNew, provider, startScan]);

  useEffect(() => {
    if (!autoStart || !accountId) {
      autoStartedAccountId.current = undefined;
      return;
    }
    if (autoStartedAccountId.current === accountId) return;
    autoStartedAccountId.current = accountId;
    void beginScan();
  }, [accountId, autoStart, beginScan]);

  const connectWorkspace = (
    <section
      id={connectWorkspaceId}
      className="wechat-connect-workspace"
      aria-labelledby={connectTitleId}
    >
      <div className="wechat-connect-workspace-head">
        <div>
          <Text className="wechat-section-kicker">{t(copy('quickConnect'))}</Text>
          <Title id={connectTitleId} level={3}>
            {hasAccounts
              ? t(copy('newConnectionTitle'))
              : t(copy('guideTitle'))}
          </Title>
          <Paragraph>
            {hasAccounts
              ? t(copy('newConnectionHint'))
              : t(copy('guideHint'))}
          </Paragraph>
        </div>
      </div>

      <div className="wechat-connect-workspace-body">
        <div className="wechat-connect-guide">
          <ol className="wechat-connect-steps">
            <li className={step >= 1 ? 'is-active' : ''}>
              <span className="wechat-step-index">1</span>
              <span className="wechat-step-icon"><MobileOutlined /></span>
              <div>
                <strong>{t(copy('stepOpenTitle'))}</strong>
                <p>{t(copy('stepOpenHint'))}</p>
              </div>
            </li>
            <li className={step >= 2 ? 'is-active' : ''}>
              <span className="wechat-step-index">2</span>
              <span className="wechat-step-icon"><QrcodeOutlined /></span>
              <div>
                <strong>{t(copy('stepScanTitle'))}</strong>
                <p>{t(copy('stepScanHint'))}</p>
              </div>
            </li>
            <li className={step >= 3 ? 'is-active' : ''}>
              <span className="wechat-step-index">3</span>
              <span className="wechat-step-icon"><SafetyCertificateOutlined /></span>
              <div>
                <strong>{t(copy('stepConfirmTitle'))}</strong>
                <p>{t(copy('stepConfirmHint'))}</p>
              </div>
            </li>
          </ol>

          <div className="wechat-security-note">
            <LockOutlined />
            <span>{t(copy('securityHint'))}</span>
          </div>
        </div>

        <div className={`wechat-scan-stage ${session ? 'has-session' : 'is-idle'}`}>
          {session ? (
            <>
              <div
                className="wechat-scan-status"
                role={session.error ? 'alert' : 'status'}
                aria-live="polite"
              >
                <span
                  className={`wechat-status-dot status-${statusColor(session.status)}`}
                  aria-hidden="true"
                />
                <div>
                  <Text strong>
                    {t(copy(`sessionStatusMap.${session.status}`), {
                      defaultValue: session.status,
                    })}
                  </Text>
                  <Paragraph>{session.message}</Paragraph>
                  {session.error ? <Paragraph type="danger">{session.error.message}</Paragraph> : null}
                </div>
              </div>

              <div className="wechat-connection-qr-wrap">
                {renderSessionVisual(session, {
                  preparing: t(copy('preparingQr')),
                  connected: t(copy('connectSuccessVisual')),
                  failed: t(copy('connectFailedVisual')),
                })}
                {session.qr?.expires_at && activeScan ? (
                  <Text type="secondary">
                    {t(copy('qrExpiresAt'), { time: formatTime(session.qr.expires_at) })}
                  </Text>
                ) : null}
              </div>

              {session.status === 'verification_required' || canAct(session, 'submit_challenge') ? (
                <div className="wechat-connection-challenge">
                  <Text strong>
                    {session.challenge?.prompt || t(copy('challengePrompt'))}
                  </Text>
                  <Space.Compact className="wechat-challenge-input">
                    <Input
                      value={challengeValue}
                      maxLength={12}
                      inputMode="numeric"
                      aria-label={t(copy('challengePrompt'))}
                      placeholder={t(copy('challengePlaceholder'))}
                      onChange={(event: ChangeEvent<HTMLInputElement>) => setChallengeValue(event.target.value)}
                      onPressEnter={() => void submitChallenge()}
                    />
                    <Button
                      type="primary"
                      loading={actionLoading}
                      onClick={() => void submitChallenge()}
                    >
                      {t(copy('submitChallenge'))}
                    </Button>
                  </Space.Compact>
                </div>
              ) : null}

              <Space wrap className="wechat-connection-scan-actions">
                {canAct(session, 'refresh') ? (
                  <Button icon={<ReloadOutlined />} loading={actionLoading} onClick={() => void refreshQr()}>
                    {t(copy('refreshQr'))}
                  </Button>
                ) : null}
                {canAct(session, 'cancel') ? (
                  <Button loading={actionLoading} onClick={() => void cancelScan()}>
                    {t(copy('cancelScan'))}
                  </Button>
                ) : null}
                {!activeScan ? (
                  <Button onClick={closeSessionPanel}>
                    {session.status === 'connected'
                      ? t(copy('addAnotherAccount'))
                      : t(copy('closePanel'))}
                  </Button>
                ) : null}
              </Space>
            </>
          ) : (
            <div className="wechat-scan-empty">
              <span className="wechat-scan-empty-icon" aria-hidden="true">{channelIcon}</span>
              <div>
                <Title level={4}>{t(copy('readyTitle'))}</Title>
                <Paragraph>{t(copy('readyHint'))}</Paragraph>
              </div>
              <Button
                type="primary"
                size="large"
                icon={<QrcodeOutlined />}
                loading={sessionStarting}
                onClick={() => void beginScan()}
              >
                {t(copy('startScan'))}
              </Button>
              <Text type="secondary">{t(copy('estimatedTime'))}</Text>
            </div>
          )}
        </div>
      </div>
    </section>
  );

  return (
    <div className={`wechat-connection-page is-${provider} is-embedded`}>
      <main className="wechat-connection-content">
        {connectWorkspace}
      </main>
    </div>
  );
}

function AccountDisclosure({ account, onReconnect, onChanged }: { account: ChannelAccount; onReconnect: () => void; onChanged: () => void }) {
  const { t } = useTranslation();
  const [detail, setDetail] = useState<AccountDetail>();
  const [refs, setRefs] = useState<Reference[]>([]);
  const [cursor, setCursor] = useState('');
  const [error, setError] = useState(false);
  const [busy, setBusy] = useState(false);
  const binding = account.binding_status || detail?.binding_status;
  const unbound = binding === 'unbound';
  const pendingActivation = isChannelAccountPendingActivation(detail || account);
  useEffect(() => {
    let active = true;
    void getAccountDetail(account.id).then(value => { if (active) setDetail(value); }).catch(() => {});
    return () => { active = false; };
  }, [account.id, account.default_recipient_id]);
  const load = async () => {
    setBusy(true); setError(false);
    try {
      const [d, r] = await Promise.all([getAccountDetail(account.id), getReferences(account.id)]);
      setDetail(d); setRefs(r.items); setCursor(r.next_cursor);
    } catch { setError(true); } finally { setBusy(false); }
  };
  const disconnect = async (unbind = false, archive = false) => {
    setBusy(true);
    try {
      // Re-read impact immediately before asking for confirmation; an unavailable dependency must not look like zero references.
      const r = await getReferences(account.id);
      Modal.confirm({ zIndex: 1600, title: t(archive ? 'notifications.removeAccountTitle' : unbind ? 'notifications.unbindTitle' : 'notifications.disconnectTitle'), content: <><p>{t(archive ? 'notifications.removeAccountHint' : unbind ? 'notifications.unbindHint' : account.provider === 'feishu' ? 'notifications.pauseFeishuHint' : 'notifications.disconnectHint')}</p><p>{t('notifications.referenceCount', { count: r.total })}</p>{r.items.map(item => <p key={item.kind + item.id}>{item.name}</p>)}</>, okButtonProps: { danger: true }, okText: t(archive ? 'notifications.removeAccount' : unbind ? 'notifications.unbind' : 'notifications.disconnect'), cancelText: t('notifications.cancel'), onOk: async () => { await (archive ? archiveChannelAccount : account.provider === 'feishu' && !unbind ? pauseChannelAccount : disconnectChannelAccount)(account.id); onChanged(); } });
    } catch { setError(true); } finally { setBusy(false); }
  };
  const reconnect = async () => {
    setBusy(true);
    try {
      await resumeChannelAccount(account.id);
      onChanged();
      message.success(t('notifications.connected'));
    } catch (error) {
      const code = (error as { response?: { data?: { error?: { code?: string } } } })?.response?.data?.error?.code;
      if (code?.endsWith('_REAUTHORIZATION_REQUIRED')) {
        message.info(t('notifications.reauthorizeHint'));
        onReconnect();
      } else {
        message.error(getLocalizedErrorMessage(error) || t('notifications.loadFailed'));
      }
    } finally { setBusy(false); }
  };
  return <details className="notification-account" onToggle={e => { if (e.currentTarget.open && !busy) void load(); }}>
    <summary><ChannelBrand channel={account.provider as ChannelProvider} avatar={account.avatar_url} /><div className="notification-grow"><strong>{channelAccountLabel(account)}</strong><small>{detail?.default_recipient?.label || t('notifications.noPrimary')} · {t('notifications.taskReferenceCount', { count: detail?.notification_reference_count || 0 })}</small></div><Tag color={pendingActivation ? 'warning' : account.status === 'connected' ? 'success' : 'default'}>{t('notifications.' + (binding === 'unbound' ? 'unbound' : pendingActivation ? 'pendingActivation' : account.status === 'connected' ? 'connected' : 'disconnected'))}</Tag></summary>
    {busy && <Spin size="small" />}
    {error && <p role="alert">{t('notifications.loadFailed')} <Button onClick={() => void load()}>{t('notifications.retry')}</Button></p>}
    {detail && <div className="notification-account-details">
      <div><small>{t('notifications.accountInformation')}</small><strong>{channelAccountLabel(account)}</strong><p>{t('notifications.primary')}：{detail.default_recipient?.label || t('notifications.noPrimary')}</p></div>
      <div><small>{t('notifications.runtime')}</small><strong>{t(`channelGateway.${account.provider}.runtimeStatusMap.${detail.runtime_status}`)}</strong><p>{t(pendingActivation ? 'notifications.pendingActivationHint' : account.status === 'connected' ? 'notifications.connectionAvailableHint' : 'notifications.connectionStoppedHint')}</p></div>
      <div><small>{t('notifications.connectedAt')}</small><strong>{formatTime(detail.connected_at)}</strong><p>{t('notifications.authorizationTimeHint')}</p></div>
      <div><small>{t('notifications.lastMessageAt')}</small><strong>{formatTime(detail.last_message_at)}</strong><p>{t('notifications.lastMessageHint')}</p></div>
      <div className="notification-wide"><small>{t('notifications.references')}</small><strong>{refs.map(r => r.name).join('、') || t('notifications.noReferences')}</strong><p>{t('notifications.referencesStableHint')}</p>
        {cursor && <Button disabled={busy} onClick={async () => { setBusy(true); try { const r = await getReferences(account.id, cursor); setRefs(old => [...old, ...r.items]); setCursor(r.next_cursor); } catch { setError(true); } finally { setBusy(false); } }}>{t('notifications.loadMore')}</Button>}
      </div>
    </div>}
    <footer><div className="notification-account-actions">{account.status === 'connected'
      ? <Button disabled={busy} danger onClick={() => void disconnect()}>{t('notifications.disconnect')}</Button>
      : <Button disabled={busy} onClick={() => void reconnect()}>{t('notifications.reconnect')}</Button>}
      {account.provider === 'feishu' && unbound && <Button disabled={busy} onClick={onReconnect}>{t('notifications.reauthorize')}</Button>}
      {account.provider === 'feishu' && unbound && <Button disabled={busy} danger onClick={() => void disconnect(false, true)}>{t('notifications.removeAccount')}</Button>}</div><small>{t('notifications.accountId')}：{account.id}</small></footer>
  </details>;
}

export function TerminalConnectionPage({ initialProvider, embedded = false, onUseAccount }: { initialProvider?: ChannelProvider; embedded?: boolean; onUseAccount?: (account: ChannelAccount) => void } = {}) {
  const { t } = useTranslation();
  const [searchParams, setSearchParams] = useSearchParams();
  const fromURL = searchParams.get('provider') as ChannelProvider;
  const [provider, setProvider] = useState<ChannelProvider>(initialProvider || (providers.includes(fromURL) ? fromURL : 'feishu'));
  const [accounts, setAccounts] = useState<ChannelAccount[]>([]);
  const [error, setError] = useState(false);
  const [loading, setLoading] = useState(true);
  const [reconnectId, setReconnectId] = useState<string>();
  const [refresh, setRefresh] = useState(0);
  const onChanged = useCallback(() => setRefresh(n => n + 1), []);
  const [choosingDefault, setChoosingDefault] = useState(false);
  const [savingDefault, setSavingDefault] = useState(false);
  const [connectedAccount, setConnectedAccount] = useState<ChannelAccount>();
  const onConnected = useCallback((account?: ChannelAccount) => { setConnectedAccount(account); setReconnectId(undefined); setRefresh(n => n + 1); }, []);
  useEffect(() => {
    let active = true;
    setLoading(true); setError(false);
    Promise.all(providers.map(listChannelAccounts)).then(results => { if (active) setAccounts(results.flatMap(r => r.items)); }).catch(() => { if (active) setError(true); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [refresh]);
  const select = (p: ChannelProvider) => {
    setProvider(p); setReconnectId(undefined); setConnectedAccount(undefined);
    if (!embedded) { const params = new URLSearchParams(searchParams); params.set('provider', p); setSearchParams(params, { replace: true }); }
  };
  return <div className="notification-connections">
    <header className="notification-heading"><LinkOutlined /><div><h2>{t('notifications.connectTitle')}</h2><p>{t('notifications.connectHint')}</p></div><Tag className="notification-connection-count"><LinkOutlined />{t('notifications.enabledCount', { count: accounts.filter(isChannelAccountAvailable).length })}</Tag></header>
    {error && <p role="alert">{t('notifications.loadFailed')}</p>}
    {connectedAccount && <Alert type="success" message={`${channelAccountLabel(connectedAccount)} · ${t('notifications.connected')}`} action={<Space><Button onClick={() => setChoosingDefault(true)}>{t('notifications.setDefaultRecipient')}</Button>{onUseAccount && <Button onClick={() => onUseAccount(connectedAccount)}>{t('notifications.returnUseAccount')}</Button>}</Space>} />}
    {choosingDefault && connectedAccount && <TargetPicker provider={connectedAccount.provider as ChannelProvider} accounts={[connectedAccount]} current={{ enabled: true, account_id: connectedAccount.id }} disabled={savingDefault} onClose={() => setChoosingDefault(false)} onSave={async target => {
      setSavingDefault(true);
      try { const updated = await setDefaultRecipient(connectedAccount.id, target.recipient_id!); setConnectedAccount(updated); setChoosingDefault(false); onChanged(); }
      catch (error) { const reason = notificationError(error).reason; message.error(t(reason === 'FEISHU_GROUPS_UNAVAILABLE' ? 'notifications.groupPermissionHint' : reason === 'NOTIFICATION_TARGET_UNAVAILABLE' ? 'notifications.recipientNoLongerAvailable' : 'notifications.defaultSaveFailed')); } finally { setSavingDefault(false); }
    }} />}
    <nav className="notification-provider-tabs" aria-label={t('notifications.channels')}>{providers.map(p => {
      const providerAccounts = accounts.filter(a => a.provider === p);
      const count = providerAccounts.filter(isChannelAccountAvailable).length;
      const pendingCount = providerAccounts.filter(isChannelAccountPendingActivation).length;
      return <button key={p} type="button" aria-pressed={provider === p} onClick={() => select(p)}><ChannelBrand channel={p} /><span><strong>{t('notifications.' + p)}</strong>{count > 0 ? <small>{t('notifications.enabledCount', { count })}</small> : pendingCount > 0 ? <small>{t('notifications.pendingActivationCount', { count: pendingCount })}</small> : null}</span><small>{t('notifications.' + (count ? 'connected' : pendingCount ? 'pendingActivation' : 'notConnected'))}</small></button>;
    })}</nav>
    <div className="notification-connection-columns"><section className="notification-account-manager"><header><div><small>{t('notifications.accountManagement')}</small><h2>{t('notifications.connectedAccounts')}</h2><p>{t('notifications.accountHint')}</p></div>{accounts.some(a => a.provider === provider && isChannelAccountAvailable(a)) && <Tag color="success">{t('notifications.availableCount', { count: accounts.filter(a => a.provider === provider && isChannelAccountAvailable(a)).length })}</Tag>}</header><div className="notification-account-list">
      {loading && <Spin />}
      {!loading && !error && !accounts.some(a => a.provider === provider) && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('notifications.noAccounts')} />}
      {accounts.filter(a => a.provider === provider).map(account => <AccountDisclosure key={account.id + account.updated_at} account={account} onChanged={onChanged} onReconnect={() => setReconnectId(account.id)} />)}
      </div><p className="notification-account-note">{t('notifications.accountRoleHint')}</p>
    </section><section className="notification-connect-pane"><header className="notification-connect-pane-heading"><div><small>{t('notifications.scanConnection')}</small><h2>{t(reconnectId ? 'notifications.reconnectPlatform' : 'notifications.connectPlatform', { platform: t('notifications.' + provider) })}</h2><p>{t('notifications.newAccountHint')}</p></div><ChannelBrand channel={provider} /></header>
      {provider !== 'feishu' && reconnectId && <Button onClick={() => setReconnectId(undefined)}>{t('notifications.newAccount')}</Button>}
      {(!loading || accounts.length > 0) && !error && <ChannelConnectionPage
        key={provider}
        provider={provider}
        accountId={reconnectId}
        autoStart={Boolean(reconnectId)}
        createNew={provider === 'feishu' && !reconnectId && accounts.some(a => a.provider === 'feishu')}
        onConnected={onConnected}
      />}
    </section></div>
  </div>;
}
