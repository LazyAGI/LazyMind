import { useMemo } from "react";
import { Button, Empty, Input, Select, Tooltip } from "antd";
import {
  AppstoreOutlined,
  BookOutlined,
  DatabaseOutlined,
  FileTextOutlined,
  StarOutlined,
  TeamOutlined,
  ToolOutlined,
} from "@ant-design/icons";
import type { StructuredAsset } from "../../shared";
import { getMarketSource } from "./skillMarketMockData";
import { filterMarketSkills } from "./skillHelpers";

interface SkillMarketViewProps {
  t: (key: string, options?: Record<string, unknown>) => string;
  loading: boolean;
  skillAssets: StructuredAsset[];
  mockInstalledUids: Set<string>;
  keyword: string;
  onKeywordChange: (value: string) => void;
  source: "all" | "builtin" | "admin";
  onSourceChange: (value: "all" | "builtin" | "admin") => void;
  category: string;
  onCategoryChange: (value: string) => void;
  categories: string[];
  onReset: () => void;
  onInstall: (item: StructuredAsset) => void;
  onUninstall: (item: StructuredAsset) => void;
  onDetail: (item: StructuredAsset) => void;
  installingUid?: string;
}

const categoryIconMap: Record<string, typeof AppstoreOutlined> = {
  research: BookOutlined,
  review: FileTextOutlined,
  search: DatabaseOutlined,
  team: TeamOutlined,
  personal: ToolOutlined,
  推荐技能: StarOutlined,
  文档处理: FileTextOutlined,
  知识库增强: DatabaseOutlined,
  业务流程: ToolOutlined,
  研发与运维: TeamOutlined,
  团队共享: TeamOutlined,
};

const getCategoryIcon = (category: string) => {
  const Icon = categoryIconMap[category.toLowerCase()] || StarOutlined;
  return <Icon />;
};

export default function SkillMarketView({
  t,
  loading,
  skillAssets,
  mockInstalledUids,
  keyword,
  onKeywordChange,
  source,
  onSourceChange,
  category,
  onCategoryChange,
  categories,
  onReset,
  onInstall,
  onUninstall,
  onDetail,
  installingUid,
}: SkillMarketViewProps) {
  const enabledBuiltinUids = useMemo(
    () =>
      new Set([
        ...skillAssets
          .filter((item) => item.originBuiltinSkillUid)
          .map((item) => item.originBuiltinSkillUid as string),
        ...mockInstalledUids,
      ]),
    [mockInstalledUids, skillAssets],
  );

  const marketItems = useMemo(
    () =>
      filterMarketSkills(skillAssets, {
        keyword,
        category,
        source,
      }),
    [category, keyword, skillAssets, source],
  );

  const recommendationItems = useMemo(
    () => marketItems.filter((item) => !enabledBuiltinUids.has(item.builtinSkillUid || "")),
    [enabledBuiltinUids, marketItems],
  );

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
                {getCategoryIcon(item)}
                {item}
              </button>
            ))}
          </div>
        </div>
      </div>

      {loading ? (
        <div className="memory-skill-market-loading">{t("common.loading")}</div>
      ) : recommendationItems.length ? (
        <section className="memory-skill-section">
          <div className="memory-skill-section-head">
            <div className="memory-skill-section-title">
              <h3>{t("admin.memorySkillMarketRecommendTitle")}</h3>
              <span className="memory-skill-section-count">
                {t("admin.memorySkillMarketRecommendCount", {
                  count: recommendationItems.length,
                })}
              </span>
            </div>
          </div>
          <div className="memory-skill-recommend-grid">
            {recommendationItems.map((item) => {
              const installed = enabledBuiltinUids.has(item.builtinSkillUid || "");
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
                  <span className="memory-skill-tile-icon">{getCategoryIcon(item.category)}</span>
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
                      <Button size="small" danger onClick={() => onUninstall(item)}>
                        {t("admin.memorySkillMarketUninstall")}
                      </Button>
                    ) : (
                      <Button
                        type="primary"
                        size="small"
                        loading={installingUid === item.builtinSkillUid}
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
  );
}
