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
  isAdmin: boolean;
  onInstall: (item: StructuredAsset) => void;
  onDetail: (item: StructuredAsset) => void;
  onDelete: (item: StructuredAsset) => void;
  installingUid?: string;
  deletingUid?: string;
  page: number;
  pageSize: number;
  total: number;
  onPageChange: (page: number, pageSize: number) => void;
}

const marketPageSizeOptions = ["12", "20", "24"];

export default function SkillMarketView({
  t,
  loading,
  skillAssets,
  installedSkills,
  isAdmin,
  onInstall,
  onDetail,
  onDelete,
  installingUid,
  deletingUid,
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
            <div className="memory-skill-recommend-grid">
              {skillAssets.map((item) => {
                const installed = isMarketSkillInstalled(installedSkills, item);
                const marketSource = getMarketSource(item);
                const sourceLabel =
                  marketSource === "admin"
                    ? t("admin.memorySkillSourceAdmin")
                    : marketSource === "builtin"
                      ? item.provider || t("admin.memorySkillSourceBuiltin")
                      : t("admin.memorySkillSourcePersonal");
                const sourceBadgeClass =
                  marketSource === "admin"
                    ? "is-admin"
                    : marketSource === "builtin"
                      ? "is-builtin"
                      : "is-personal";

                return (
                  <article
                    key={item.id}
                    className={`memory-skill-tile ${installed ? "is-installed" : ""}`}
                  >
                    <div className="memory-skill-tile-top">
                      <span className="memory-skill-tile-icon">
                        {renderSkillCategoryIcon(item.tags[0] || item.category || "default")}
                      </span>
                      <span className={`memory-skill-badge memory-skill-badge-source ${sourceBadgeClass}`}>
                        {sourceLabel}
                      </span>
                    </div>
                    <strong className="memory-skill-tile-name">{item.name}</strong>
                    <Tooltip title={item.description}>
                      <p className="memory-skill-tile-desc">{item.description}</p>
                    </Tooltip>
                    <div className="memory-skill-tile-foot">
                      <div className="memory-skill-tile-actions">
                        <Button size="small" onClick={() => onDetail(item)}>
                          {t("admin.memorySkillMarketDetail")}
                        </Button>
                        {installed ? (
                          <Button size="small" disabled className="memory-skill-installed-action">
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
                        {isAdmin && marketSource === "admin" ? (
                          <Button
                            danger
                            size="small"
                            loading={deletingUid === item.id}
                            disabled={Boolean(deletingUid) && deletingUid !== item.id}
                            onClick={() => onDelete(item)}
                          >
                            {t("admin.memorySkillMarketDelete")}
                          </Button>
                        ) : null}
                      </div>
                    </div>
                  </article>
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
