import { useState, type ChangeEvent } from 'react';
import {
  Button,
  Empty,
  Input,
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
  LockOutlined,
  MobileOutlined,
  QrcodeOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  UnorderedListOutlined,
  WechatOutlined,
} from '@ant-design/icons';
import dayjs from 'dayjs';

import type {
  ChannelAccount,
  ChannelProvider,
  ConnectionSession,
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

function canAct(session: ConnectionSession | null, action: string): boolean {
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

export default function ChannelConnectionPage({
  provider,
}: ChannelConnectionPageProps) {
  const [accountsPanelOpen, setAccountsPanelOpen] = useState(false);
  const translationKey = `channelGateway.${provider}`;
  const copy = (name: string) => `${translationKey}.${name}`;
  const channelIcon = provider === 'wechat'
    ? <WechatOutlined />
    : (
      <img
        className="feishu-official-icon"
        src="/feishu-official.svg"
        alt=""
        aria-hidden="true"
      />
    );
  const {
    t,
    accounts,
    accountsLoading,
    session,
    sessionStarting,
    actionLoading,
    disconnectingAccountId,
    challengeValue,
    setChallengeValue,
    loadAccounts,
    startScan,
    cancelScan,
    disconnectAccount,
    refreshQr,
    submitChallenge,
    closeSessionPanel,
  } = useChannelConnection(provider);

  const step = currentStep(session);
  const hasAccounts = accounts.length > 0;
  const activeScan = isActiveScan(session);
  const accountsPanelId = `${provider}-accounts-panel`;
  const accountsTitleId = `${provider}-accounts-title`;
  const connectWorkspaceId = `${provider}-connect-workspace`;
  const connectTitleId = `${provider}-connect-title`;

  const beginScan = () => {
    void startScan();
  };

  const columns: ColumnsType<ChannelAccount> = [
    {
      title: t(copy('accountLabel')),
      dataIndex: 'label',
      key: 'label',
      width: 240,
      render: (value: string) => (
        <div className="wechat-account-name">
          <span>{channelIcon}</span>
          <Tooltip title={value || '-'}>
            <strong>{value || '-'}</strong>
          </Tooltip>
        </div>
      ),
    },
    {
      title: t(copy('accountStatus')),
      dataIndex: 'status',
      key: 'status',
      width: 140,
      render: (value: string) => (
        <Tag color={statusColor(value)}>
          {t(copy(`accountStatusMap.${value}`), { defaultValue: value })}
        </Tag>
      ),
    },
    {
      title: t(copy('runtimeStatus')),
      dataIndex: 'runtime_status',
      key: 'runtime_status',
      width: 140,
      render: (value: string) => (
        <Tag color={statusColor(value)}>
          {t(copy(`runtimeStatusMap.${value}`), { defaultValue: value })}
        </Tag>
      ),
    },
    {
      title: t(copy('connectedAt')),
      dataIndex: 'connected_at',
      key: 'connected_at',
      width: 180,
      render: formatTime,
    },
    {
      title: t(copy('lastMessageAt')),
      dataIndex: 'last_message_at',
      key: 'last_message_at',
      width: 180,
      render: formatTime,
    },
    {
      title: t(copy('lastError')),
      dataIndex: 'last_error',
      key: 'last_error',
      width: 200,
      ellipsis: true,
      render: (value: string | null) =>
        value ? (
          <Tooltip title={value} placement="top" overlayStyle={{ maxWidth: 360 }}>
            <span className="wechat-error-cell">{value}</span>
          </Tooltip>
        ) : '-',
    },
    {
      title: t(copy('actions')),
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
              title: t(copy('disconnectConfirmTitle')),
              content: t(copy('disconnectConfirmContent'), {
                account: account.label,
              }),
              okText: t(copy('disconnectConfirmOk')),
              cancelText: t('common.cancel'),
              okButtonProps: { danger: true },
              onOk: () => disconnectAccount(account.id),
            });
          }}
        >
          {t(copy('disconnectAccount'))}
        </Button>
      ),
    },
  ];

  const accountsSection = (
    <section
      id={accountsPanelId}
      className="wechat-connection-accounts"
      aria-labelledby={accountsTitleId}
    >
      <div className="wechat-connection-accounts-head">
        <div>
          <div className="wechat-accounts-title-row">
            <Title id={accountsTitleId} level={4}>
              {t(copy('accountsTitle'))}
            </Title>
            {!accountsLoading ? <span>{accounts.length}</span> : null}
          </div>
          <Text type="secondary">{t(copy('accountsHint'))}</Text>
        </div>
        <Space wrap className="wechat-accounts-actions">
          <Button
            icon={<ReloadOutlined />}
            loading={accountsLoading}
            onClick={() => void loadAccounts()}
          >
            {t(copy('refreshAccounts'))}
          </Button>
        </Space>
      </div>

      <Table<ChannelAccount>
        rowKey="id"
        loading={accountsLoading}
        columns={columns}
        dataSource={accounts}
        pagination={false}
        scroll={{ x: 1190 }}
        locale={{
          emptyText: (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={t(copy('accountsEmpty'))}
            />
          ),
        }}
      />
    </section>
  );

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
    <div className={`wechat-connection-page is-${provider}`}>
      <header className="wechat-connection-header">
        <div className="wechat-connection-heading">
          <span aria-hidden="true">{channelIcon}</span>
          <div>
            <Title level={2}>{t(copy('title'))}</Title>
            <Paragraph>{t(copy('subtitle'))}</Paragraph>
          </div>
        </div>
        <Button
          aria-controls={accountsPanelId}
          aria-haspopup="dialog"
          icon={<UnorderedListOutlined />}
          onClick={() => setAccountsPanelOpen(true)}
        >
          {t(copy('viewAccounts'), { count: accounts.length })}
        </Button>
      </header>

      <main className="wechat-connection-content">
        {connectWorkspace}
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
        {accountsSection}
      </Modal>
    </div>
  );
}

export function WechatConnectionPage() {
  return <ChannelConnectionPage provider="wechat" />;
}

export function FeishuConnectionPage() {
  return <ChannelConnectionPage provider="feishu" />;
}
