import { Card, Col, Row, Typography } from "antd";

const { Text } = Typography;

export interface DataSourceSummaryCardsProps {
  t: any;
  total: number;
  activeCount: number;
  scheduledCount: number;
  totalDocuments: number;
  warningCount: number;
}

export default function DataSourceSummaryCards({
  t,
  total,
  activeCount,
  scheduledCount,
  totalDocuments,
  warningCount,
}: DataSourceSummaryCardsProps) {
  return (
    <Row gutter={[16, 16]}>
      <Col xs={24} sm={12} lg={6}>
        <Card className="data-source-summary-card">
          <Text type="secondary">{t("admin.dataSourceCardTotal")}</Text>
          <div className="data-source-summary-value">{total}</div>
          <Text type="secondary">{t("admin.dataSourceCardTotalHint")}</Text>
        </Card>
      </Col>
      <Col xs={24} sm={12} lg={6}>
        <Card className="data-source-summary-card">
          <Text type="secondary">{t("admin.dataSourceCardActive")}</Text>
          <div className="data-source-summary-value">{activeCount}</div>
          <Text type="secondary">
            {t("admin.dataSourceCardActiveHint", { count: scheduledCount })}
          </Text>
        </Card>
      </Col>
      <Col xs={24} sm={12} lg={6}>
        <Card className="data-source-summary-card">
          <Text type="secondary">{t("admin.dataSourceCardDocs")}</Text>
          <div className="data-source-summary-value">{totalDocuments}</div>
          <Text type="secondary">{t("admin.dataSourceCardDocsHint")}</Text>
        </Card>
      </Col>
      <Col xs={24} sm={12} lg={6}>
        <Card className="data-source-summary-card warning">
          <Text type="secondary">{t("admin.dataSourceCardAlert")}</Text>
          <div className="data-source-summary-value">{warningCount}</div>
          <Text type="secondary">{t("admin.dataSourceCardAlertHint")}</Text>
        </Card>
      </Col>
    </Row>
  );
}
