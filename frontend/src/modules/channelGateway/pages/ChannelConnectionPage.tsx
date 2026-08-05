import { useCallback, useEffect, useState, type ChangeEvent } from 'react';
import {
  Button,
  Empty,
  Input,
  message,
  Modal,
  QRCode,
  Space,
  Spin,
  Table,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  CheckCircleFilled,
  CloseCircleFilled,
  LinkOutlined,
  LockOutlined,
  MobileOutlined,
  QrcodeOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  UnorderedListOutlined,
  WechatOutlined,
} from '@ant-design/icons';
import dayjs from 'dayjs';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';

import type {
  ChannelAccount,
  ChannelProvider,
  ConnectionSession,
} from '../api';
import {
  disconnectChannelAccount,
  listChannelAccounts,
} from '../api';
import { useChannelConnection } from '../hooks/useChannelConnection';
import './channelConnectionPage.scss';

const { Paragraph, Text, Title } = Typography;

function ChannelIcon({ provider }: { provider: ChannelProvider }) {
  return provider === 'wechat'
    ? <WechatOutlined />
    : (
      <img
        className="feishu-official-icon"
        src="/feishu-official.svg"
        alt=""
        aria-hidden="true"
      />
    );
}

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
}

function ChannelConnectionPage({ provider }: ChannelConnectionPageProps) {
  const translationKey = `channelGateway.${provider}`;
  const copy = (name: string) => `${translationKey}.${name}`;
  const channelIcon = <ChannelIcon provider={provider} />;
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

  const step = currentStep(session);
  const hasAccounts = accounts.length > 0;
  const activeScan = isActiveScan(session);
  const connectWorkspaceId = `${provider}-connect-workspace`;
  const connectTitleId = `${provider}-connect-title`;

  const beginScan = () => {
    void startScan();
  };

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
                onClick={beginScan}
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

function accountProvider(account: ChannelAccount): ChannelProvider {
  return account.provider === 'feishu' ? 'feishu' : 'wechat';
}

export function TerminalConnectionPage() {
  const { t } = useTranslation();
  const [searchParams, setSearchParams] = useSearchParams();
  const [accountsPanelOpen, setAccountsPanelOpen] = useState(false);
  const [accounts, setAccounts] = useState<ChannelAccount[]>([]);
  const [accountsLoading, setAccountsLoading] = useState(true);
  const [disconnectingAccountId, setDisconnectingAccountId] = useState<string | null>(null);
  const provider: ChannelProvider = (
    searchParams.get('provider') === 'feishu' ? 'feishu' : 'wechat'
  );

  const loadAccounts = useCallback(async () => {
    setAccountsLoading(true);
    try {
      const [wechatAccounts, feishuAccounts] = await Promise.all([
        listChannelAccounts('wechat'),
        listChannelAccounts('feishu'),
      ]);
      setAccounts(
        [...wechatAccounts.items, ...feishuAccounts.items]
          .sort((left, right) => (
            dayjs(right.updated_at).valueOf() - dayjs(left.updated_at).valueOf()
          )),
      );
    } catch {
      message.error(t('channelGateway.terminal.loadAccountsFailed'));
    } finally {
      setAccountsLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void loadAccounts();
  }, [loadAccounts]);

  const selectProvider = (nextProvider: ChannelProvider) => {
    setSearchParams({ provider: nextProvider }, { replace: true });
  };

  const openAccountsPanel = () => {
    setAccountsPanelOpen(true);
    void loadAccounts();
  };

  const disconnectAccount = async (account: ChannelAccount) => {
    setDisconnectingAccountId(account.id);
    try {
      await disconnectChannelAccount(account.id);
      message.success(t('channelGateway.terminal.disconnectSuccess'));
      await loadAccounts();
    } catch {
      message.error(t('channelGateway.terminal.disconnectFailed'));
    } finally {
      setDisconnectingAccountId(null);
    }
  };

  const columns: ColumnsType<ChannelAccount> = [
    {
      title: t('channelGateway.terminal.provider'),
      dataIndex: 'provider',
      key: 'provider',
      width: 140,
      render: (_value: string, account) => {
        const rowProvider = accountProvider(account);
        return (
          <div className="terminal-account-provider">
            <span className={`terminal-provider-icon is-${rowProvider}`} aria-hidden="true">
              <ChannelIcon provider={rowProvider} />
            </span>
            <strong>{t(`channelGateway.terminal.${rowProvider}Title`)}</strong>
          </div>
        );
      },
    },
    {
      title: t('channelGateway.terminal.accountLabel'),
      dataIndex: 'label',
      key: 'label',
      width: 240,
      render: (value: string, account) => {
        const rowProvider = accountProvider(account);
        return (
          <div className={`wechat-account-name is-${rowProvider}`}>
            <span aria-hidden="true"><ChannelIcon provider={rowProvider} /></span>
            <Tooltip title={value || '-'}>
              <strong>{value || '-'}</strong>
            </Tooltip>
          </div>
        );
      },
    },
    {
      title: t('channelGateway.terminal.accountStatus'),
      dataIndex: 'status',
      key: 'status',
      width: 140,
      render: (value: string, account) => {
        const rowProvider = accountProvider(account);
        return (
          <Tag color={statusColor(value)}>
            {t(`channelGateway.${rowProvider}.accountStatusMap.${value}`, {
              defaultValue: value,
            })}
          </Tag>
        );
      },
    },
    {
      title: t('channelGateway.terminal.runtimeStatus'),
      dataIndex: 'runtime_status',
      key: 'runtime_status',
      width: 140,
      render: (value: string, account) => {
        const rowProvider = accountProvider(account);
        return (
          <Tag color={statusColor(value)}>
            {t(`channelGateway.${rowProvider}.runtimeStatusMap.${value}`, {
              defaultValue: value,
            })}
          </Tag>
        );
      },
    },
    {
      title: t('channelGateway.terminal.connectedAt'),
      dataIndex: 'connected_at',
      key: 'connected_at',
      width: 180,
      render: formatTime,
    },
    {
      title: t('channelGateway.terminal.lastMessageAt'),
      dataIndex: 'last_message_at',
      key: 'last_message_at',
      width: 180,
      render: formatTime,
    },
    {
      title: t('channelGateway.terminal.lastError'),
      dataIndex: 'last_error',
      key: 'last_error',
      width: 220,
      ellipsis: true,
      render: (value: string | null) =>
        value ? (
          <Tooltip title={value} placement="top" overlayStyle={{ maxWidth: 360 }}>
            <span className="wechat-error-cell">{value}</span>
          </Tooltip>
        ) : '-',
    },
    {
      title: t('channelGateway.terminal.actions'),
      key: 'actions',
      fixed: 'right',
      width: 110,
      render: (_value, account) => (
        <Button
          danger
          type="link"
          loading={disconnectingAccountId === account.id}
          onClick={() => {
            Modal.confirm({
              title: t('channelGateway.terminal.disconnectConfirmTitle'),
              content: t('channelGateway.terminal.disconnectConfirmContent', {
                account: account.label,
              }),
              okText: t('channelGateway.terminal.disconnectConfirmOk'),
              cancelText: t('common.cancel'),
              okButtonProps: { danger: true },
              onOk: () => disconnectAccount(account),
            });
          }}
        >
          {t('channelGateway.terminal.disconnectAccount')}
        </Button>
      ),
    },
  ];

  return (
    <div className="terminal-connection-page">
      <header className="terminal-connection-header">
        <span className="terminal-connection-icon" aria-hidden="true">
          <LinkOutlined />
        </span>
        <div>
          <Title level={2}>{t('channelGateway.terminal.title')}</Title>
          <Paragraph>{t('channelGateway.terminal.subtitle')}</Paragraph>
        </div>
        <Button
          className="terminal-accounts-trigger"
          aria-controls="terminal-accounts-panel"
          aria-haspopup="dialog"
          icon={<UnorderedListOutlined />}
          loading={accountsLoading && accounts.length === 0}
          onClick={openAccountsPanel}
        >
          {t('channelGateway.terminal.viewAccounts', { count: accounts.length })}
        </Button>
      </header>

      <main className="terminal-connection-content">
        <nav
          className="terminal-provider-switch"
          aria-label={t('channelGateway.terminal.providerLabel')}
        >
          {(['wechat', 'feishu'] as ChannelProvider[]).map((item) => (
            <button
              key={item}
              type="button"
              className={item === provider ? 'is-active' : ''}
              aria-pressed={item === provider}
              onClick={() => selectProvider(item)}
            >
              <span className={`terminal-provider-icon is-${item}`} aria-hidden="true">
                <ChannelIcon provider={item} />
              </span>
              <span>
                <strong>{t(`channelGateway.terminal.${item}Title`)}</strong>
                <small>{t(`channelGateway.terminal.${item}Hint`)}</small>
              </span>
            </button>
          ))}
        </nav>

        <ChannelConnectionPage
          key={provider}
          provider={provider}
        />
      </main>

      <Modal
        className="wechat-accounts-modal"
        open={accountsPanelOpen}
        width={1200}
        footer={null}
        destroyOnClose
        centered
        onCancel={() => setAccountsPanelOpen(false)}
      >
        <section
          id="terminal-accounts-panel"
          className="wechat-connection-accounts"
          aria-labelledby="terminal-accounts-title"
        >
          <div className="wechat-connection-accounts-head">
            <div>
              <div className="wechat-accounts-title-row">
                <Title id="terminal-accounts-title" level={4}>
                  {t('channelGateway.terminal.accountsTitle')}
                </Title>
                {!accountsLoading ? <span>{accounts.length}</span> : null}
              </div>
              <Text type="secondary">{t('channelGateway.terminal.accountsHint')}</Text>
            </div>
            <Space wrap className="wechat-accounts-actions">
              <Button
                icon={<ReloadOutlined />}
                loading={accountsLoading}
                onClick={() => void loadAccounts()}
              >
                {t('channelGateway.terminal.refreshAccounts')}
              </Button>
            </Space>
          </div>

          <Table<ChannelAccount>
            rowKey="id"
            loading={accountsLoading}
            columns={columns}
            dataSource={accounts}
            pagination={false}
            scroll={{ x: 1330 }}
            locale={{
              emptyText: (
                <Empty
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  description={t('channelGateway.terminal.accountsEmpty')}
                />
              ),
            }}
          />
        </section>
      </Modal>
    </div>
  );
}
