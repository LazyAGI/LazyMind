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

import type { ChannelAccount, ConnectionSession } from '../api';
import { useWechatConnection } from '../hooks/useWechatConnection';
import './wechatConnectionPage.scss';

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

export default function WechatConnectionPage() {
  const [accountsPanelOpen, setAccountsPanelOpen] = useState(false);
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
  } = useWechatConnection();

  const step = currentStep(session);
  const hasAccounts = accounts.length > 0;
  const activeScan = isActiveScan(session);

  const beginScan = () => {
    void startScan();
  };

  const columns: ColumnsType<ChannelAccount> = [
    {
      title: t('channelGateway.wechat.accountLabel'),
      dataIndex: 'label',
      key: 'label',
      render: (value: string) => (
        <div className="wechat-account-name">
          <span><WechatOutlined /></span>
          <strong>{value || '-'}</strong>
        </div>
      ),
    },
    {
      title: t('channelGateway.wechat.accountStatus'),
      dataIndex: 'status',
      key: 'status',
      width: 140,
      render: (value: string) => (
        <Tag color={statusColor(value)}>
          {t(`channelGateway.wechat.accountStatusMap.${value}`, { defaultValue: value })}
        </Tag>
      ),
    },
    {
      title: t('channelGateway.wechat.runtimeStatus'),
      dataIndex: 'runtime_status',
      key: 'runtime_status',
      width: 140,
      render: (value: string) => (
        <Tag color={statusColor(value)}>
          {t(`channelGateway.wechat.runtimeStatusMap.${value}`, { defaultValue: value })}
        </Tag>
      ),
    },
    {
      title: t('channelGateway.wechat.connectedAt'),
      dataIndex: 'connected_at',
      key: 'connected_at',
      width: 180,
      render: formatTime,
    },
    {
      title: t('channelGateway.wechat.lastMessageAt'),
      dataIndex: 'last_message_at',
      key: 'last_message_at',
      width: 180,
      render: formatTime,
    },
    {
      title: t('channelGateway.wechat.lastError'),
      dataIndex: 'last_error',
      key: 'last_error',
      ellipsis: true,
      render: (value: string | null) =>
        value ? (
          <Tooltip title={value} placement="top" overlayStyle={{ maxWidth: 360 }}>
            <span className="wechat-error-cell">{value}</span>
          </Tooltip>
        ) : '-',
    },
    {
      title: t('channelGateway.wechat.actions'),
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
              title: t('channelGateway.wechat.disconnectConfirmTitle'),
              content: t('channelGateway.wechat.disconnectConfirmContent', {
                account: account.label,
              }),
              okText: t('channelGateway.wechat.disconnectConfirmOk'),
              cancelText: t('common.cancel'),
              okButtonProps: { danger: true },
              onOk: () => disconnectAccount(account.id),
            });
          }}
        >
          {t('channelGateway.wechat.disconnectAccount')}
        </Button>
      ),
    },
  ];

  const accountsSection = (
    <section
      id="wechat-accounts-panel"
      className="wechat-connection-accounts"
      aria-labelledby="wechat-accounts-title"
    >
      <div className="wechat-connection-accounts-head">
        <div>
          <div className="wechat-accounts-title-row">
            <Title id="wechat-accounts-title" level={4}>
              {t('channelGateway.wechat.accountsTitle')}
            </Title>
            {!accountsLoading ? <span>{accounts.length}</span> : null}
          </div>
          <Text type="secondary">{t('channelGateway.wechat.accountsHint')}</Text>
        </div>
        <Space wrap className="wechat-accounts-actions">
          <Button
            icon={<ReloadOutlined />}
            loading={accountsLoading}
            onClick={() => void loadAccounts()}
          >
            {t('channelGateway.wechat.refreshAccounts')}
          </Button>
        </Space>
      </div>

      <Table<ChannelAccount>
        rowKey="id"
        loading={accountsLoading}
        columns={columns}
        dataSource={accounts}
        pagination={false}
        scroll={{ x: 940 }}
        locale={{
          emptyText: (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={t('channelGateway.wechat.accountsEmpty')}
            />
          ),
        }}
      />
    </section>
  );

  const connectWorkspace = (
    <section
      id="wechat-connect-workspace"
      className="wechat-connect-workspace"
      aria-labelledby="wechat-connect-title"
    >
      <div className="wechat-connect-workspace-head">
        <div>
          <Text className="wechat-section-kicker">{t('channelGateway.wechat.quickConnect')}</Text>
          <Title id="wechat-connect-title" level={3}>
            {hasAccounts
              ? t('channelGateway.wechat.newConnectionTitle')
              : t('channelGateway.wechat.guideTitle')}
          </Title>
          <Paragraph>
            {hasAccounts
              ? t('channelGateway.wechat.newConnectionHint')
              : t('channelGateway.wechat.guideHint')}
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
                <strong>{t('channelGateway.wechat.stepOpenTitle')}</strong>
                <p>{t('channelGateway.wechat.stepOpenHint')}</p>
              </div>
            </li>
            <li className={step >= 2 ? 'is-active' : ''}>
              <span className="wechat-step-index">2</span>
              <span className="wechat-step-icon"><QrcodeOutlined /></span>
              <div>
                <strong>{t('channelGateway.wechat.stepScanTitle')}</strong>
                <p>{t('channelGateway.wechat.stepScanHint')}</p>
              </div>
            </li>
            <li className={step >= 3 ? 'is-active' : ''}>
              <span className="wechat-step-index">3</span>
              <span className="wechat-step-icon"><SafetyCertificateOutlined /></span>
              <div>
                <strong>{t('channelGateway.wechat.stepConfirmTitle')}</strong>
                <p>{t('channelGateway.wechat.stepConfirmHint')}</p>
              </div>
            </li>
          </ol>

          <div className="wechat-security-note">
            <LockOutlined />
            <span>{t('channelGateway.wechat.securityHint')}</span>
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
                    {t(`channelGateway.wechat.sessionStatusMap.${session.status}`, {
                      defaultValue: session.status,
                    })}
                  </Text>
                  <Paragraph>{session.message}</Paragraph>
                  {session.error ? <Paragraph type="danger">{session.error.message}</Paragraph> : null}
                </div>
              </div>

              <div className="wechat-connection-qr-wrap">
                {renderSessionVisual(session, {
                  preparing: t('channelGateway.wechat.preparingQr'),
                  connected: t('channelGateway.wechat.connectSuccessVisual'),
                  failed: t('channelGateway.wechat.connectFailedVisual'),
                })}
                {session.qr?.expires_at && activeScan ? (
                  <Text type="secondary">
                    {t('channelGateway.wechat.qrExpiresAt', { time: formatTime(session.qr.expires_at) })}
                  </Text>
                ) : null}
              </div>

              {session.status === 'verification_required' || canAct(session, 'submit_challenge') ? (
                <div className="wechat-connection-challenge">
                  <Text strong>
                    {session.challenge?.prompt || t('channelGateway.wechat.challengePrompt')}
                  </Text>
                  <Space.Compact className="wechat-challenge-input">
                    <Input
                      value={challengeValue}
                      maxLength={12}
                      inputMode="numeric"
                      aria-label={t('channelGateway.wechat.challengePrompt')}
                      placeholder={t('channelGateway.wechat.challengePlaceholder')}
                      onChange={(event: ChangeEvent<HTMLInputElement>) => setChallengeValue(event.target.value)}
                      onPressEnter={() => void submitChallenge()}
                    />
                    <Button
                      type="primary"
                      loading={actionLoading}
                      onClick={() => void submitChallenge()}
                    >
                      {t('channelGateway.wechat.submitChallenge')}
                    </Button>
                  </Space.Compact>
                </div>
              ) : null}

              <Space wrap className="wechat-connection-scan-actions">
                {canAct(session, 'refresh') ? (
                  <Button icon={<ReloadOutlined />} loading={actionLoading} onClick={() => void refreshQr()}>
                    {t('channelGateway.wechat.refreshQr')}
                  </Button>
                ) : null}
                {canAct(session, 'cancel') ? (
                  <Button loading={actionLoading} onClick={() => void cancelScan()}>
                    {t('channelGateway.wechat.cancelScan')}
                  </Button>
                ) : null}
                {!activeScan ? (
                  <Button onClick={closeSessionPanel}>
                    {session.status === 'connected'
                      ? t('channelGateway.wechat.addAnotherAccount')
                      : t('channelGateway.wechat.closePanel')}
                  </Button>
                ) : null}
              </Space>
            </>
          ) : (
            <div className="wechat-scan-empty">
              <span className="wechat-scan-empty-icon" aria-hidden="true"><WechatOutlined /></span>
              <div>
                <Title level={4}>{t('channelGateway.wechat.readyTitle')}</Title>
                <Paragraph>{t('channelGateway.wechat.readyHint')}</Paragraph>
              </div>
              <Button
                type="primary"
                size="large"
                icon={<QrcodeOutlined />}
                loading={sessionStarting}
                onClick={beginScan}
              >
                {t('channelGateway.wechat.startScan')}
              </Button>
              <Text type="secondary">{t('channelGateway.wechat.estimatedTime')}</Text>
            </div>
          )}
        </div>
      </div>
    </section>
  );

  return (
    <div className="wechat-connection-page">
      <header className="wechat-connection-header">
        <div className="wechat-connection-heading">
          <span aria-hidden="true"><WechatOutlined /></span>
          <div>
            <Title level={2}>{t('channelGateway.wechat.title')}</Title>
            <Paragraph>{t('channelGateway.wechat.subtitle')}</Paragraph>
          </div>
        </div>
        <Button
          aria-controls="wechat-accounts-panel"
          aria-haspopup="dialog"
          icon={<UnorderedListOutlined />}
          onClick={() => setAccountsPanelOpen(true)}
        >
          {t('channelGateway.wechat.viewAccounts', { count: accounts.length })}
        </Button>
      </header>

      <main className="wechat-connection-content">
        {connectWorkspace}
      </main>

      <Modal
        className="wechat-accounts-modal"
        open={accountsPanelOpen}
        width={1120}
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
