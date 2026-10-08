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
import { getAccountDetail, getReferences, providers, type AccountDetail, type Reference } from '@/modules/notifications/api';
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
  renameChannelAccount,
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

export function ChannelConnectionPage({ provider, accountId, createNew, autoStart, onConnected }: ChannelConnectionPageProps) {
  const translationKey = `channelGateway.${provider}`;
  const restoringWecom = provider === 'wecom' && Boolean(accountId);
  const [botId, setBotId] = useState('');
  const [botSecret, setBotSecret] = useState('');
  const [refreshingAuthorization, setRefreshingAuthorization] = useState(false);
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

  useEffect(() => {
    if (session?.status === 'connected') {
      setBotSecret('');
      onConnected?.(session.account || undefined);
    }
  }, [session?.status, session?.account, onConnected]);
  useEffect(() => { setBotId(''); setBotSecret(''); }, [provider, accountId]);
  const step = currentStep(session);
  const hasAccounts = accounts.length > 0;
  const activeScan = isActiveScan(session);
  const connectWorkspaceId = `${provider}-connect-workspace`;
  const connectTitleId = `${provider}-connect-title`;
  const autoStartedAccountId = useRef<string>();

  const beginScan = useCallback(() => {
    // Generic WeCom QR authorization can provision a new bot. Do not start it
    // implicitly when the user is restoring an existing account.
    if (restoringWecom) return;
    return startScan({ accountId, ...(provider === 'feishu' && accountId ? { reauthorize: true } : {}), ...(createNew ? { createNew: true } : {}) });
  }, [accountId, createNew, provider, restoringWecom, startScan]);

  useEffect(() => {
    if (!autoStart || !accountId || restoringWecom) {
      autoStartedAccountId.current = undefined;
      return;
    }
    if (autoStartedAccountId.current === accountId) return;
    autoStartedAccountId.current = accountId;
    void beginScan();
  }, [accountId, autoStart, beginScan, restoringWecom]);

  if (restoringWecom) {
    const originalAccount = accounts.find(account => account.id === accountId);
    return <section className="notification-wecom-connect wecom-original-connection" aria-labelledby="wecom-restore-title">
      <Title id="wecom-restore-title" level={4}>{t('notifications.wecomRestorePermissionTitle')}</Title>
      {originalAccount && <Paragraph>{t('notifications.account')}：{channelAccountLabel(originalAccount, accounts)}</Paragraph>}
      <Alert type="info" showIcon message={t('notifications.wecomReauthorizeStepHint')} />
      <Button type="primary" loading={refreshingAuthorization} disabled={sessionStarting} onClick={async () => {
        if (!accountId || refreshingAuthorization) return;
        setRefreshingAuthorization(true);
        try {
          const account = await resumeChannelAccount(accountId, { silentError: true });
          onConnected?.(account);
        } catch (error) {
          message.error(getLocalizedErrorMessage(error) || t('notifications.loadFailed'));
        } finally { setRefreshingAuthorization(false); }
      }}>{t('notifications.wecomRefreshAuthorization')}</Button>
      <details className="wecom-original-credentials">
        <summary>{t('notifications.wecomUpdateCredentials')}</summary>
        <Title level={5}>{t('notifications.wecomRestoreCredentialsTitle')}</Title>
        <Paragraph>{t('notifications.wecomRestoreCredentialsHint')}</Paragraph>
        <Alert type="warning" showIcon message={t('notifications.wecomUseOriginalCredentials')} />
        <form onSubmit={event => {
          event.preventDefault();
          if (!botId.trim() || !botSecret.trim() || sessionStarting || refreshingAuthorization) return;
          void startScan({ accountId, credentials: { bot_id: botId.trim(), secret: botSecret.trim() } });
        }}>
          <label htmlFor="wecom-original-bot-id">{t('notifications.wecomBotId')}</label>
          <Input id="wecom-original-bot-id" value={botId} autoComplete="off" onChange={event => setBotId(event.target.value)} />
          <label htmlFor="wecom-original-bot-secret">{t('notifications.wecomBotSecret')}</label>
          <Input.Password id="wecom-original-bot-secret" value={botSecret} autoComplete="new-password" onChange={event => setBotSecret(event.target.value)} />
          <Button type="primary" htmlType="submit" loading={sessionStarting} disabled={refreshingAuthorization || !botId.trim() || !botSecret.trim()}>{t('notifications.wecomRestoreConnection')}</Button>
        </form>
      </details>
      {session?.status === 'connected' && <Alert type="success" message={t('notifications.connected')} />}
      {session?.error && <Alert type="error" message={session.error.message} />}
    </section>;
  }

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

function AccountDisclosure({ account, accounts, onReconnect, onChanged }: { account: ChannelAccount; accounts: ChannelAccount[]; onReconnect: () => void; onChanged: () => void }) {
  const { t } = useTranslation();
  const [detail, setDetail] = useState<AccountDetail>();
  const [refs, setRefs] = useState<Reference[]>([]);
  const [cursor, setCursor] = useState('');
  const [error, setError] = useState(false);
  const [busy, setBusy] = useState(false);
  const [editingRemark, setEditingRemark] = useState(false);
  const [remark, setRemark] = useState('');
  const [savingRemark, setSavingRemark] = useState(false);
  const binding = account.binding_status || detail?.binding_status;
  const unbound = binding === 'unbound';
  const provisioning = account.status === 'provisioning';
  const pendingActivation = isChannelAccountPendingActivation(account);
  useEffect(() => {
    let active = true;
    void getAccountDetail(account.id).then(value => { if (active) setDetail(value); }).catch(() => {});
    return () => { active = false; };
  }, [account.id, account.status, account.runtime_status, account.capabilities?.notification_ready]);
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
      await resumeChannelAccount(account.id, { silentError: true });
      onChanged();
      message.success(t('notifications.connected'));
    } catch (error) {
      const code = (error as { response?: { data?: { error?: { code?: string } } } })?.response?.data?.error?.code;
      if (code?.endsWith('_REAUTHORIZATION_REQUIRED') || code === 'WECOM_CAPABILITY_REAUTH_REQUIRED') {
        message.info(t(account.provider === 'wecom' ? 'notifications.wecomReauthorizeHint' : 'notifications.reauthorizeHint'));
        onReconnect();
      } else {
        message.error(getLocalizedErrorMessage(error) || t('notifications.loadFailed'));
      }
    } finally { setBusy(false); }
  };
  return <details className="notification-account" onToggle={e => { if (e.currentTarget.open && !busy) void load(); }}>
    <summary><ChannelBrand channel={account.provider as ChannelProvider} avatar={account.avatar_url} /><div className="notification-grow"><strong>{channelAccountLabel(account, accounts)}</strong><small>{t('notifications.taskReferenceCount', { count: detail?.notification_reference_count || 0 })}</small></div><Tag color={provisioning || pendingActivation ? 'warning' : account.status === 'connected' ? 'success' : 'default'}>{t('notifications.' + (provisioning ? 'connecting' : binding === 'unbound' ? 'unbound' : pendingActivation ? 'pendingActivation' : account.status === 'connected' ? 'connected' : 'disconnected'))}</Tag></summary>
    {busy && <Spin size="small" />}
    {error && <p role="alert">{t('notifications.loadFailed')} <Button onClick={() => void load()}>{t('notifications.retry')}</Button></p>}
    {detail && <div className="notification-account-details">
      <div><small>{t('notifications.accountInformation')}</small><strong>{channelAccountLabel(account, accounts)}</strong></div>
      {account.provider === 'wecom' && <div><small>{t('notifications.wecomBotIdentity')}</small><strong>{detail.identity?.bot_id || account.identity?.bot_id || t('notifications.identityMissing')}</strong></div>}
      <div><small>{t('notifications.runtime')}</small><strong>{t(`channelGateway.${account.provider}.runtimeStatusMap.${detail.runtime_status}`)}</strong><p>{t(provisioning ? 'notifications.connectionInProgressHint' : pendingActivation ? 'notifications.pendingActivationHint' : account.status === 'connected' ? 'notifications.connectionAvailableHint' : 'notifications.connectionStoppedHint')}</p></div>
      <div><small>{t('notifications.connectedAt')}</small><strong>{formatTime(detail.connected_at)}</strong><p>{t('notifications.authorizationTimeHint')}</p></div>
      <div><small>{t('notifications.lastMessageAt')}</small><strong>{formatTime(detail.last_message_at)}</strong><p>{t('notifications.lastMessageHint')}</p></div>
      <div className="notification-wide"><small>{t('notifications.references')}</small><strong>{refs.map(r => r.name).join('、') || t('notifications.noReferences')}</strong><p>{t('notifications.referencesStableHint')}</p>
        {cursor && <Button disabled={busy} onClick={async () => { setBusy(true); try { const r = await getReferences(account.id, cursor); setRefs(old => [...old, ...r.items]); setCursor(r.next_cursor); } catch { setError(true); } finally { setBusy(false); } }}>{t('notifications.loadMore')}</Button>}
      </div>
    </div>}
    <footer><div className="notification-account-actions">{provisioning ? null : account.status === 'connected'
      ? <Button disabled={busy} danger onClick={() => void disconnect()}>{t('notifications.disconnect')}</Button>
      : <Button disabled={busy} onClick={() => void reconnect()}>{t('notifications.reconnect')}</Button>}
      {account.provider === 'feishu' && unbound && <Button disabled={busy} onClick={onReconnect}>{t('notifications.reauthorize')}</Button>}
      {account.provider === 'wecom' && !provisioning && <Button disabled={busy || savingRemark} onClick={onReconnect}>{t('notifications.wecomRepairAuthorization')}</Button>}
      {account.provider === 'wecom' && <Button disabled={busy || savingRemark} onClick={() => { setRemark(account.label); setEditingRemark(true); }}>{t('notifications.editAccountLabel')}</Button>}
      {account.provider === 'feishu' && unbound && <Button disabled={busy} danger onClick={() => void disconnect(false, true)}>{t('notifications.removeAccount')}</Button>}</div>{account.provider === 'wecom' ? <small>{t('notifications.wecomReauthorizeHint')}</small> : <small>{t('notifications.accountId')}：{account.id}</small>}</footer>
    {account.provider === 'wecom' && <Modal open={editingRemark} destroyOnHidden zIndex={1600} title={t('notifications.editAccountLabel')} okText={t('notifications.saveChanges')} cancelText={t('notifications.cancel')} confirmLoading={savingRemark} okButtonProps={{ disabled: !remark.trim() }} onCancel={() => { if (!savingRemark) setEditingRemark(false); }} onOk={async () => {
      if (!remark.trim() || savingRemark) return;
      setSavingRemark(true);
      try {
        await renameChannelAccount(account.id, remark.trim());
        setEditingRemark(false);
        message.success(t('notifications.renameAccountSuccess'));
        onChanged();
      } catch (error) {
        message.error(getLocalizedErrorMessage(error) || t('notifications.saveFailed'));
      } finally { setSavingRemark(false); }
    }}>
      <Paragraph>{t('notifications.wecomAccountRemarkHint')}</Paragraph>
      <label htmlFor={`wecom-remark-${account.id}`}>{t('notifications.accountRemark')}</label>
      <Input id={`wecom-remark-${account.id}`} value={remark} maxLength={80} autoFocus onChange={event => setRemark(event.target.value)} />
    </Modal>}
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
  const [connectedAccount, setConnectedAccount] = useState<ChannelAccount>();
  const onConnected = useCallback((account?: ChannelAccount) => { setConnectedAccount(account); setReconnectId(undefined); setRefresh(n => n + 1); }, []);
  useEffect(() => {
    let active = true;
    setLoading(true); setError(false);
    Promise.all(providers.map(listChannelAccounts)).then(results => { if (active) setAccounts(results.flatMap(r => r.items)); }).catch(() => { if (active) setError(true); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [refresh]);
  useEffect(() => {
    if (loading || !accounts.some(account => account.status === 'provisioning' || isChannelAccountPendingActivation(account))) return;
    const timer = window.setTimeout(onChanged, 2000);
    return () => window.clearTimeout(timer);
  }, [accounts, loading, onChanged]);
  const select = (p: ChannelProvider) => {
    setProvider(p); setReconnectId(undefined); setConnectedAccount(undefined);
    if (!embedded) { const params = new URLSearchParams(searchParams); params.set('provider', p); setSearchParams(params, { replace: true }); }
  };
  return <div className="notification-connections">
    <header className="notification-heading"><LinkOutlined /><div><h2>{t('notifications.connectTitle')}</h2><p>{t('notifications.connectHint')}</p></div><Tag className="notification-connection-count"><LinkOutlined />{t('notifications.enabledCount', { count: accounts.filter(isChannelAccountAvailable).length })}</Tag></header>
    {error && <p role="alert">{t('notifications.loadFailed')}</p>}
    {connectedAccount && <Alert type="success" message={`${channelAccountLabel(connectedAccount, accounts)} · ${t('notifications.connected')}`} action={onUseAccount && <Button onClick={() => onUseAccount(connectedAccount)}>{t('notifications.returnUseAccount')}</Button>} />}
    <nav className="notification-provider-tabs" aria-label={t('notifications.channels')}>{providers.map(p => {
      const providerAccounts = accounts.filter(a => a.provider === p);
      const count = providerAccounts.filter(isChannelAccountAvailable).length;
      const pendingCount = providerAccounts.filter(isChannelAccountPendingActivation).length;
      const connecting = providerAccounts.some(account => account.status === 'provisioning');
      return <button key={p} type="button" aria-pressed={provider === p} onClick={() => select(p)}><ChannelBrand channel={p} /><span><strong>{t('notifications.' + p)}</strong>{count > 0 ? <small>{t('notifications.enabledCount', { count })}</small> : connecting ? <small>{t('notifications.connecting')}</small> : pendingCount > 0 ? <small>{t('notifications.pendingActivationCount', { count: pendingCount })}</small> : null}</span><small>{t('notifications.' + (count ? 'connected' : connecting ? 'connecting' : pendingCount ? 'pendingActivation' : 'notConnected'))}</small></button>;
    })}</nav>
    <div className="notification-connection-columns"><section className="notification-account-manager"><header><div><small>{t('notifications.accountManagement')}</small><h2>{t('notifications.connectedAccounts')}</h2><p>{t('notifications.accountHint')}</p></div>{accounts.some(a => a.provider === provider && isChannelAccountAvailable(a)) && <Tag color="success">{t('notifications.availableCount', { count: accounts.filter(a => a.provider === provider && isChannelAccountAvailable(a)).length })}</Tag>}</header><div className="notification-account-list">
      {loading && <Spin />}
      {!loading && !error && !accounts.some(a => a.provider === provider) && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('notifications.noAccounts')} />}
      {accounts.filter(a => a.provider === provider).map(account => <AccountDisclosure key={account.id + account.updated_at} account={account} accounts={accounts} onChanged={onChanged} onReconnect={() => setReconnectId(account.id)} />)}
      </div><p className="notification-account-note">{t('notifications.accountRoleHint')}</p>
    </section><section className="notification-connect-pane"><header className="notification-connect-pane-heading"><div><small>{t(reconnectId && provider === 'wecom' ? 'notifications.accountManagement' : 'notifications.scanConnection')}</small><h2>{t(reconnectId && provider === 'wecom' ? 'notifications.wecomRepairAuthorization' : reconnectId ? 'notifications.reconnectPlatform' : 'notifications.connectPlatform', { platform: t('notifications.' + provider) })}</h2><p>{t(reconnectId && provider === 'wecom' ? 'notifications.wecomReauthorizeHint' : 'notifications.newAccountHint')}</p></div><ChannelBrand channel={provider} /></header>
      {provider !== 'feishu' && reconnectId && <Button onClick={() => setReconnectId(undefined)}>{t('notifications.newAccount')}</Button>}
      {(!loading || accounts.length > 0) && !error && <ChannelConnectionPage
        key={provider === 'wecom' ? `${provider}:${reconnectId || 'new'}` : provider}
        provider={provider}
        accountId={reconnectId}
        autoStart={Boolean(reconnectId)}
        createNew={provider === 'feishu' && !reconnectId && accounts.some(a => a.provider === 'feishu')}
        onConnected={onConnected}
      />}
    </section></div>
  </div>;
}
