import { Button, Empty, Input, Pagination, Select, Tooltip } from "antd";
import { AppstoreOutlined } from "@ant-design/icons";
import type { StructuredAsset } from "../../shared";
import { getMarketSource } from "./skillMarketMockData";
import { isMarketSkillInstalled } from "./skillHelpers";
import { renderSkillCategoryIcon } from "./skillCategoryIcon";

interface SkillMarketViewProps {
  t: (key: string, options?: Record<string, unknown>) => string;
  loading: boolean;
  skillAssets: StructuredAsset[];
  installedSkills: StructuredAsset[];
  keyword: string;
  onKeywordChange: (value: string) => void;
  source: "all" | "builtin" | "admin";
  onSourceChange: (value: "all" | "builtin" | "admin") => void;
  category: string;
  onCategoryChange: (value: string) => void;
  categories: string[];
  onReset: () => void;
  onInstall: (item: StructuredAsset) => void;
  onDetail: (item: StructuredAsset) => void;
  installingUid?: string;
  page: number;
  pageSize: number;
  total: number;
  onPageChange: (page: number, pageSize: number) => void;
}

const marketPageSizeOptions = ["8", "12", "20"];

export default function SkillMarketView({
  t,
  loading,
  skillAssets,
  installedSkills,
  keyword,
  onKeywordChange,
  source,
  onSourceChange,
  category,
  onCategoryChange,
  categories,
  onReset,
  onInstall,
  onDetail,
  installingUid,
  page,
  pageSize,
  total,
  onPageChange,
}: SkillMarketViewProps) {
  return (
    <div className="memory-skill-market">
      <div className="memory-skill-filter-strip">
        <div className="memory-skill-filter-controls">
          <Input
            allowClear
            value={keyword}
            onChange={(event) => onKeywordChange(event.target.value)}
            placeholder={t("admin.memorySkillMarketSearchPlaceholder")}
            className="memory-skill-market-search"
          />
          <Select
            value={source}
            className="memory-skill-market-source"
            options={[
              { value: "all", label: t("admin.memorySkillMarketSourceAll") },
              { value: "builtin", label: t("admin.memorySkillMarketSourceBuiltin") },
              { value: "admin", label: t("admin.memorySkillMarketSourceAdmin") },
            ]}
            onChange={onSourceChange}
          />
          <Button onClick={onReset}>{t("admin.memoryReset")}</Button>
        </div>

        <div className="memory-skill-filter-meta-row">
          <div className="memory-skill-category-bar">
            <button
              type="button"
              className={`memory-skill-category-pill ${category === "all" ? "is-active" : ""}`}
              onClick={() => onCategoryChange("all")}
            >
              <AppstoreOutlined />
              {t("admin.memorySkillCategoryAll")}
            </button>
            {categories.map((item) => (
              <button
                key={item}
                type="button"
                className={`memory-skill-category-pill ${category === item ? "is-active" : ""}`}
                onClick={() => onCategoryChange(item)}
              >
                {renderSkillCategoryIcon(item)}
                {item}
              </button>
            ))}
          </div>
        </div>
      </div>

      <div className="memory-skill-market-scroll">
        {loading ? (
          <div className="memory-skill-market-loading">{t("common.loading")}</div>
        ) : skillAssets.length ? (
          <section className="memory-skill-section">
            <div className="memory-skill-section-head">
              <div className="memory-skill-section-title">
                <h3>{t("admin.memorySkillMarketRecommendTitle")}</h3>
                <span className="memory-skill-section-count">
                  {t("admin.memorySkillMarketRecommendCount", {
                    count: total,
                  })}
                </span>
              </div>
            </div>
            <div className="memory-skill-recommend-grid">
              {skillAssets.map((item) => {
                const installed = isMarketSkillInstalled(installedSkills, item);
                const marketSource = getMarketSource(item);
                const sourceLabel =
                  marketSource === "admin"
                    ? t("admin.memorySkillSourceAdmin")
                    : marketSource === "builtin"
                      ? t("admin.memorySkillSourceBuiltin")
                      : t("admin.memorySkillSourcePersonal");
                const sourceBadgeClass =
                  marketSource === "admin"
                    ? "is-admin"
                    : marketSource === "builtin"
                      ? "is-builtin"
                      : "is-personal";

                return (
                  <div
                    key={item.id}
                    className={`memory-skill-tile ${installed ? "is-installed" : ""}`}
                  >
                    <span className="memory-skill-tile-icon">{renderSkillCategoryIcon(item.category)}</span>
                    <div className="memory-skill-tile-copy">
                      <div className="memory-skill-tile-title-line">
                        <strong className="memory-skill-tile-name">{item.name}</strong>
                        <div className="memory-skill-tile-badges">
                          {installed ? (
                            <span className="memory-skill-badge memory-skill-badge-installed">
                              {t("admin.memorySkillInstalledBadge")}
                            </span>
                          ) : null}
                          <span className={`memory-skill-badge memory-skill-badge-source ${sourceBadgeClass}`}>
                            {sourceLabel}
                          </span>
                        </div>
                      </div>
                      <Tooltip title={item.description}>
                        <p className="memory-skill-tile-desc">{item.description}</p>
                      </Tooltip>
                      {item.category ? (
                        <div className="memory-skill-tile-meta">
                          <span className="memory-skill-tile-category">{item.category}</span>
                        </div>
                      ) : null}
                    </div>
                    <div className="memory-skill-tile-actions">
                      <Button size="small" onClick={() => onDetail(item)}>
                        {t("admin.memorySkillMarketDetail")}
                      </Button>
                      {installed ? (
                        <Button size="small" disabled>
                          {t("admin.memorySkillInstalledBadge")}
                        </Button>
                      ) : (
                        <Button
                          type="primary"
                          size="small"
                          loading={installingUid === item.id}
                          onClick={() => onInstall(item)}
                        >
                          {t("admin.memorySkillMarketInstall")}
                        </Button>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          </section>
        ) : (
          <Empty
            className="memory-skill-market-empty"
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description={t("admin.memorySkillMarketEmpty")}
          />
        )}
      </div>

      {total > 0 ? (
        <div className="memory-skill-market-pagination">
          <Pagination
            current={page}
            pageSize={pageSize}
            total={total}
            showSizeChanger
            pageSizeOptions={marketPageSizeOptions}
            showTotal={(itemTotal) => t("common.totalItems", { total: itemTotal })}
            onChange={onPageChange}
            onShowSizeChange={(_current, nextPageSize) => onPageChange(1, nextPageSize)}
          />
        </div>
      ) : null}
    </div>
  );
}
