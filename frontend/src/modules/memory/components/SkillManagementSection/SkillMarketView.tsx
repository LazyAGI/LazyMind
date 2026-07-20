import { Button, Empty, Pagination, Tooltip } from "antd";
import type { StructuredAsset } from "../../shared";
import { getMarketSource } from "./skillMarketMockData";
import { isMarketSkillInstalled } from "./skillHelpers";
import { renderSkillCategoryIcon } from "./skillCategoryIcon";

interface SkillMarketViewProps {
  t: (key: string, options?: Record<string, unknown>) => string;
  loading: boolean;
  skillAssets: StructuredAsset[];
  installedSkills: StructuredAsset[];
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
