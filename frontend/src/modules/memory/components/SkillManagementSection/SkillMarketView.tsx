import { useLayoutEffect, useRef } from "react";
import { Button, Empty, Pagination, Select, Tooltip } from "antd";
import { CheckOutlined, PlusOutlined } from "@ant-design/icons";
import type { StructuredAsset } from "../../shared";
import { getMarketSource } from "./skillMarketMockData";
import { isMarketSkillInstalled } from "./skillHelpers";
import SkillIcon from "./SkillIcon";
import { getMarketSkillIcon } from "./skillMarketIcons";
import SkillModuleState from "./SkillModuleState";
import { getMarketCategoryPresentation, groupMarketSkills } from "./skillMarketCategories";

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
  pageCount: number;
  onColumnsChange: (columns: number) => void;
  onPageChange: (page: number, pageSize: number) => void;
}

const marketPageSizeOptions = [12, 20, 24];

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
  pageCount,
  onColumnsChange,
  onPageChange,
}: SkillMarketViewProps) {
  const groups = groupMarketSkills(skillAssets);
  const scrollRef = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const content = scrollRef.current;
    if (!content || loading || !skillAssets.length) return;
    const measure = () => {
      const grid = content.querySelector('.memory-skill-recommend-grid');
      if (!grid) return;
      const tracks = getComputedStyle(grid).gridTemplateColumns;
      if (tracks && tracks !== 'none') onColumnsChange(tracks.split(/\s+/).length);
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(content);
    return () => observer.disconnect();
  }, [loading, skillAssets.length, onColumnsChange]);
  return (
    <div className="memory-skill-market">
      <div className="memory-skill-market-scroll" ref={scrollRef}>
        {loading ? (
          <div className="memory-skill-market-loading">
            <SkillModuleState tone="loading">{t("common.loading")}</SkillModuleState>
          </div>
        ) : skillAssets.length ? (
          <div className="memory-skill-market-groups">
            {groups.map(({ category, skills }) => {
              const presentation = getMarketCategoryPresentation(category, t);
              return (
                <section key={category} className={`memory-skill-section is-${presentation.tone}`} aria-label={presentation.label}>
                  <header className="memory-skill-market-group-heading">
                    <h3 className="memory-skill-market-category-capsule">
                      <span aria-hidden="true"><SkillIcon name={presentation.icon} size={16} /></span>
                      {presentation.label}
                    </h3>
                    <span className="memory-skill-market-group-line" aria-hidden="true" />
                    <span className="memory-skill-market-group-count">
                      {t("admin.memorySkillMarketPageCategoryCount", { count: skills.length })}
                    </span>
                  </header>
                  <div className="memory-skill-recommend-grid">
                    {skills.map((item) => {
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
                          <span className="memory-skill-tile-icon" aria-hidden="true">
                            <SkillIcon name={getMarketSkillIcon(item)} size={20} />
                          </span>
                          <Tooltip title={item.name} mouseEnterDelay={1} trigger={["hover", "focus"]}>
                            <button type="button" className="memory-skill-tile-name" onClick={() => onDetail(item)}>
                              {item.name}
                            </button>
                          </Tooltip>
                          <Tooltip title={item.description}>
                            <p className="memory-skill-tile-desc">{item.description}</p>
                          </Tooltip>
                          <div className="memory-skill-tile-foot">
                            <Tooltip title={sourceLabel}>
                              <span className={`memory-skill-market-provider ${sourceBadgeClass}`}>{sourceLabel}</span>
                            </Tooltip>
                            <div className="memory-skill-tile-actions">
                              <Button type="text" size="small" onClick={() => onDetail(item)}>
                                {t("admin.memorySkillMarketDetail")}
                              </Button>
                              {installed ? (
                                <Button size="small" disabled icon={<CheckOutlined aria-hidden="true" />} className="memory-skill-installed-action">
                                  {t("admin.memorySkillInstalledBadge")}
                                </Button>
                              ) : (
                                <Button
                                  className="memory-skill-market-install"
                                  icon={<PlusOutlined aria-hidden="true" />}
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
              );
            })}
          </div>
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
            pageSize={1}
            total={pageCount}
            showSizeChanger={false}
            showTotal={() => t("common.totalItems", { total })}
            onChange={(nextPage) => onPageChange(nextPage, pageSize)}
          />
          <Tooltip title={t("admin.memorySkillMarketPageSizeHint")}>
            <Select
              aria-label={t("admin.memorySkillMarketPageSizeLabel")}
              value={pageSize}
              options={marketPageSizeOptions.map(value => ({
                value, label: t("admin.memorySkillMarketPageSize", { count: value }),
              }))}
              onChange={(nextPageSize) => onPageChange(1, nextPageSize)}
            />
          </Tooltip>
        </div>
      ) : null}
    </div>
  );
}
