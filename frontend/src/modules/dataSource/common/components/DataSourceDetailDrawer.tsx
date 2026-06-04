import {
  Alert,
  Button,
  Card,
  Col,
  Descriptions,
  Divider,
  Drawer,
  List,
  Row,
  Space,
  Tag,
  Timeline,
  Typography,
} from "antd";
import {
  CheckCircleFilled,
  ClockCircleOutlined,
  WarningFilled,
} from "@ant-design/icons";
import type { DataSourceItem } from "../../shared";
import {
  getConflictPolicyLabel,
  getConnectionMeta,
  getFileUpdateMeta,
  getSourceTypeTitle,
  getStatusMeta,
} from "../../shared";

const { Paragraph, Text } = Typography;

export interface DataSourceDetailDrawerProps {
  t: any;
  open: boolean;
  source: DataSourceItem | undefined;
  onClose: () => void;
  onEdit: (source: DataSourceItem) => void;
}

export default function DataSourceDetailDrawer({
  t,
  open,
  source,
  onClose,
  onEdit,
}: DataSourceDetailDrawerProps) {
  return (
    <Drawer
      width={560}
      open={open}
      title={source?.name || t("admin.dataSourceDetailTitle")}
      onClose={onClose}
      extra={
        source ? (
          <Space>
            <Button onClick={() => onEdit(source)}>{t("admin.dataSourceEditConfig")}</Button>
          </Space>
        ) : null
      }
    >
      {source ? (
        <div className="data-source-drawer">
          <Space wrap size={[8, 8]}>
            <Tag>{getSourceTypeTitle(source.type, t)}</Tag>
            <Tag color={getStatusMeta(source.status, t).color}>
              {getStatusMeta(source.status, t).text}
            </Tag>
            <Tag color={getConnectionMeta(source.connectionState, t).color}>
              {t("admin.dataSourceConnectionTag", {
                status: getConnectionMeta(source.connectionState, t).text,
              })}
            </Tag>
          </Space>

          <Paragraph className="data-source-drawer-desc">{source.description}</Paragraph>

          <Descriptions column={1} size="small" className="data-source-drawer-descriptions">
            <Descriptions.Item label={t("admin.dataSourceTableKnowledgeBase")}>
              {source.knowledgeBase}
            </Descriptions.Item>
            <Descriptions.Item label={t("admin.dataSourceAccessTarget")}>
              {source.target}
            </Descriptions.Item>
            <Descriptions.Item label={t("admin.dataSourceSyncModeTitle")}>
              {source.scheduleLabel}
            </Descriptions.Item>
            <Descriptions.Item label={t("admin.dataSourceTableLastSync")}>
              {source.lastSync}
            </Descriptions.Item>
            <Descriptions.Item label={t("admin.dataSourceNextRun")}>
              {source.nextSync}
            </Descriptions.Item>
            <Descriptions.Item label={t("admin.dataSourceConflictPolicy")}>
              {getConflictPolicyLabel(source.conflictPolicy, t)}
            </Descriptions.Item>
            <Descriptions.Item label={t("admin.dataSourcePermissionScope")}>
              <Space wrap>
                {source.permissions.map((item) => (
                  <Tag key={item}>{item}</Tag>
                ))}
              </Space>
            </Descriptions.Item>
            {source.oauthConnection?.accountName ? (
              <Descriptions.Item label={t("admin.dataSourceConnectedAccount")}>
                {source.oauthConnection.accountName}
              </Descriptions.Item>
            ) : null}
            {source.oauthConnection?.connectedAt ? (
              <Descriptions.Item label={t("admin.dataSourceConnectedAt")}>
                {source.oauthConnection.connectedAt}
              </Descriptions.Item>
            ) : null}
            {source.oauthConnection?.expiresAt ? (
              <Descriptions.Item label={t("admin.dataSourceTokenExpireAt")}>
                {source.oauthConnection.expiresAt}
              </Descriptions.Item>
            ) : null}
          </Descriptions>

          {source.warning ? (
            <Alert
              showIcon
              type={
                source.status === "expired" || source.status === "error" ? "warning" : "info"
              }
              message={t("admin.dataSourceNotes")}
              description={source.warning}
            />
          ) : null}

          <Divider orientation="left">{t("admin.dataSourceSyncOverview")}</Divider>
          <Row gutter={[12, 12]}>
            <Col span={8}>
              <Card size="small" className="data-source-mini-card">
                <Text type="secondary">{t("admin.dataSourceDocTotal")}</Text>
                <div className="data-source-mini-value">{source.documentCount}</div>
              </Card>
            </Col>
            <Col span={8}>
              <Card size="small" className="data-source-mini-card">
                <Text type="secondary">{t("admin.dataSourceRecentAdded")}</Text>
                <div className="data-source-mini-value">{source.addCount}</div>
              </Card>
            </Col>
            <Col span={8}>
              <Card size="small" className="data-source-mini-card">
                <Text type="secondary">{t("admin.dataSourceRecentChanged")}</Text>
                <div className="data-source-mini-value">{source.changeCount}</div>
              </Card>
            </Col>
          </Row>

          <Divider orientation="left">{t("admin.dataSourceRecentSyncLogs")}</Divider>
          <Timeline
            items={source.logs.map((log) => ({
              color:
                log.result === "success"
                  ? "green"
                  : log.result === "warning"
                    ? "orange"
                    : "red",
              dot:
                log.result === "success" ? (
                  <CheckCircleFilled />
                ) : log.result === "warning" ? (
                  <ClockCircleOutlined />
                ) : (
                  <WarningFilled />
                ),
              children: (
                <div className="data-source-log-item">
                  <div className="data-source-log-title">{log.title}</div>
                  <div className="data-source-log-time">{log.time}</div>
                  <div className="data-source-log-description">{log.description}</div>
                </div>
              ),
            }))}
          />

          <Divider orientation="left">{t("admin.dataSourceUpdateQueue")}</Divider>
          <List
            size="small"
            dataSource={source.fileCandidates}
            renderItem={(candidate) => {
              const updateMeta = getFileUpdateMeta(candidate.updateState, t);
              return (
                <List.Item>
                  <div className="data-source-selected-file">
                    <Text strong>{candidate.name}</Text>
                    <Text type="secondary">{candidate.path}</Text>
                  </div>
                  <Tag color={updateMeta.color}>{updateMeta.text}</Tag>
                </List.Item>
              );
            }}
          />

          <Divider orientation="left">{t("admin.dataSourceSyncScope")}</Divider>
          <Text type="secondary">{t("admin.dataSourceSyncScopeHint")}</Text>
        </div>
      ) : null}
    </Drawer>
  );
}
