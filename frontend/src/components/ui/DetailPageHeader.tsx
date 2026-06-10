import { Breadcrumb, type BreadcrumbProps, Button, Tooltip } from "antd";
import { LeftOutlined } from "@ant-design/icons";
import { useStyles } from "./useStyles";

const headerCss = `
.common-detail-page-header {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.common-detail-page-header .ant-breadcrumb {
  max-width: 100%;
}
.detail-breadcrumb-item {
  display: inline-block;
  max-width: 360px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: bottom;
}
.detail-page-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-width: 0;
  flex-wrap: nowrap;
}
.detail-page-title-main,
.detail-page-title-actions {
  display: flex;
  align-items: center;
  min-width: 0;
}
.detail-page-title-main {
  flex: 1 1 auto;
  gap: 10px;
}
.detail-page-title-actions {
  flex: 0 0 auto;
  gap: 12px;
  justify-content: flex-end;
  max-width: 50%;
  overflow: hidden;
}
.detail-title {
  font-size: 20px;
  font-weight: 600;
  min-width: 0;
  flex: 1 1 auto;
}
.detail-title-text,
.detail-page-description-text,
.detail-breadcrumb-text,
.title-extra-text {
  display: inline-block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: bottom;
}
.detail-title-text {
  max-width: 100%;
}
.detail-breadcrumb-text {
  max-width: min(60vw, 960px);
}
.title-extra, .detail-page-description { font-size: 14px; color: #666; }
.title-extra {
  min-width: 0;
  display: flex;
  align-items: center;
  flex: 0 1 auto;
}
.title-extra-text {
  max-width: 320px;
}
.detail-page-description-text {
  max-width: 100%;
}
.settings-menu {
  display: flex;
  align-items: center;
  flex: 0 0 auto;
  min-width: max-content;
}
.settings-menu > div {
  display: flex;
  align-items: center;
  gap: 8px;
}
.settings-menu button {
  margin-left: 0 !important;
}
.extra-content { display: flex; flex-wrap: wrap; gap: 8px 24px; }
.extra-content-item { display: flex; align-items: center; gap: 8px; min-width: 0; max-width: 100%; }
.extra-content-label { font-size: 12px; color: #666; }
.extra-content-value { font-size: 12px; min-width: 0; max-width: 100%; }
.extra-content-value-text {
  display: inline-block;
  max-width: 360px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
@media (max-width: 640px) {
  .detail-page-title {
    gap: 8px;
  }
  .detail-page-title-main {
    gap: 8px;
  }
  .detail-page-title-actions {
    display: none;
  }
}
`;

interface ContentItem {
  label: React.ReactNode | string;
  value: React.ReactNode | string;
  hidden?: boolean;
}

export interface DetailPageHeaderProps {
  className?: string;
  breadcrumbs?: BreadcrumbProps["items"];
  title?: React.ReactNode | string;
  titleExtra?: React.ReactNode | string;
  description?: React.ReactNode | string;
  showBackButton?: boolean;
  settingsMenu?: React.ReactNode;
  extraContent?: ContentItem[];
  extraSplitter?: string;
  onBack?: () => void;
}

export default function DetailPageHeader({
  className = "",
  breadcrumbs,
  title,
  titleExtra,
  description,
  showBackButton = true,
  settingsMenu,
  extraContent,
  extraSplitter = "",
  onBack,
}: DetailPageHeaderProps) {
  useStyles("detail-page-header-styles", headerCss);
  const normalizedBreadcrumbs = breadcrumbs?.map((item) => {
    if (typeof item?.title !== "string" && typeof item?.title !== "number") {
      return item;
    }
    const fullText = String(item.title);
    return {
      ...item,
      title: (
        <Tooltip title={fullText}>
          <span className="detail-breadcrumb-item">{fullText}</span>
        </Tooltip>
      ),
    };
  });

  const renderTextWithTooltip = (
    value?: React.ReactNode | string,
    className?: string,
  ) => {
    if (typeof value !== "string" && typeof value !== "number") {
      return value;
    }
    const fullText = String(value);
    return (
      <Tooltip title={fullText}>
        <span className={className}>{fullText}</span>
      </Tooltip>
    );
  };

  return (
    <div className={`common-detail-page-header ${className}`}>
      {normalizedBreadcrumbs && normalizedBreadcrumbs.length > 0 && (
        <Breadcrumb items={normalizedBreadcrumbs} />
      )}
      <div className="detail-page-title">
        <div className="detail-page-title-main">
          {showBackButton && (
            <Button
              type="primary"
              ghost
              icon={<LeftOutlined />}
              onClick={() => (onBack ? onBack() : window.history.back())}
            />
          )}
          <span className="detail-title">
            {renderTextWithTooltip(title, "detail-title-text")}
          </span>
        </div>
        {(settingsMenu || titleExtra) && (
          <div className="detail-page-title-actions">
            {settingsMenu && <div className="settings-menu">{settingsMenu}</div>}
            {titleExtra && (
              <div className="title-extra">
                {renderTextWithTooltip(titleExtra, "title-extra-text")}
              </div>
            )}
          </div>
        )}
      </div>
      {extraContent && extraContent.length > 0 && (
        <div className="extra-content">
          {extraContent
            .filter((item) => !item.hidden)
            .map((item, index) => (
              <div key={index} className="extra-content-item">
                <span className="extra-content-label">
                  {item.label}
                  {extraSplitter}
                </span>
                <span className="extra-content-value">
                  {renderTextWithTooltip(item.value, "extra-content-value-text")}
                </span>
              </div>
            ))}
        </div>
      )}
      {description && (
        <div className="detail-page-description">
          {renderTextWithTooltip(description, "detail-page-description-text")}
        </div>
      )}
    </div>
  );
}
