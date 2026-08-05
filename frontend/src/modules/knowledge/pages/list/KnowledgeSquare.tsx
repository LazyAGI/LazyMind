import {
  useMemo,
  useState,
  type ChangeEvent,
  type MouseEvent,
  type ReactNode,
} from "react";
import { Button, Input, Modal, Select } from "antd";
import {
  AuditOutlined,
  BankOutlined,
  BookOutlined,
  BuildOutlined,
  ClockCircleOutlined,
  DatabaseOutlined,
  ExperimentOutlined,
  FileTextOutlined,
  FundOutlined,
  GlobalOutlined,
  MedicineBoxOutlined,
  SearchOutlined,
  ShoppingOutlined,
  TeamOutlined,
  ThunderboltOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";

import {
  filterOfficialKnowledgeBases,
  OFFICIAL_KNOWLEDGE_BASES,
  type KnowledgeSquareStatusMap,
  type KnowledgeSquareType,
  type OfficialKnowledgeBase,
} from "./knowledgeSquareData";

type InstallStatusFilter = "all" | "installed" | "uninstalled" | "update";

interface KnowledgeSquareProps {
  statusMap: KnowledgeSquareStatusMap;
  onInstall: (item: OfficialKnowledgeBase) => void;
  onUpdate: (item: OfficialKnowledgeBase) => void;
  onOpen: (item: OfficialKnowledgeBase) => void;
  onQuery: (item: OfficialKnowledgeBase) => void;
}

const iconByType: Record<string, ReactNode> = {
  law: <AuditOutlined />,
  finance: <FundOutlined />,
  government: <BankOutlined />,
  medical: <MedicineBoxOutlined />,
  education: <BookOutlined />,
  internet: <GlobalOutlined />,
  people: <TeamOutlined />,
  manufacturing: <BuildOutlined />,
  retail: <ShoppingOutlined />,
  energy: <ThunderboltOutlined />,
  evaluation: <ExperimentOutlined />,
};

export default function KnowledgeSquare({
  statusMap,
  onInstall,
  onUpdate,
  onOpen,
  onQuery,
}: KnowledgeSquareProps) {
  const { t } = useTranslation();
  const [type, setType] = useState<KnowledgeSquareType>("industry");
  const [domain, setDomain] = useState("全部");
  const [status, setStatus] = useState<InstallStatusFilter>("all");
  const [keyword, setKeyword] = useState("");
  const [detailItem, setDetailItem] = useState<OfficialKnowledgeBase | null>(null);

  const domains = useMemo(
    () => [
      "全部",
      ...Array.from(
        new Set(
          OFFICIAL_KNOWLEDGE_BASES.filter((item) => item.type === type).map(
            (item) => item.domain,
          ),
        ),
      ),
    ],
    [type],
  );

  const visibleItems = useMemo(
    () =>
      filterOfficialKnowledgeBases({
        items: OFFICIAL_KNOWLEDGE_BASES,
        type,
        domain,
        status,
        keyword,
        statusMap,
      }),
    [domain, keyword, status, statusMap, type],
  );

  const setActiveType = (nextType: KnowledgeSquareType) => {
    setType(nextType);
    setDomain("全部");
  };

  const resetFilters = () => {
    setKeyword("");
    setStatus("all");
    setDomain("全部");
  };

  return (
    <div className="knowledge-square-view">
      <div className="knowledge-square-type-tabs" role="tablist" aria-label={t("knowledge.squareTypeTabs")}>
        <button
          className={type === "industry" ? "is-active" : ""}
          type="button"
          role="tab"
          aria-selected={type === "industry"}
          onClick={() => setActiveType("industry")}
        >
          {t("knowledge.industryKnowledge")}
        </button>
        <button
          className={type === "evaluation" ? "is-active" : ""}
          type="button"
          role="tab"
          aria-selected={type === "evaluation"}
          onClick={() => setActiveType("evaluation")}
        >
          {t("knowledge.evaluationKnowledge")}
        </button>
      </div>

      <div className="knowledge-square-toolbar">
        <Input
          allowClear
          value={keyword}
          prefix={<SearchOutlined />}
          placeholder={t("knowledge.squareSearchPlaceholder")}
          aria-label={t("knowledge.squareSearchPlaceholder")}
          onChange={(event: ChangeEvent<HTMLInputElement>) => setKeyword(event.target.value)}
        />
        <Select<InstallStatusFilter>
          value={status}
          aria-label={t("knowledge.installStatus")}
          options={[
            { value: "all", label: t("knowledge.allStatuses") },
            { value: "uninstalled", label: t("knowledge.uninstalled") },
            { value: "installed", label: t("knowledge.installed") },
            { value: "update", label: t("knowledge.updateAvailable") },
          ]}
          onChange={setStatus}
        />
        <Button onClick={resetFilters}>{t("common.reset")}</Button>
      </div>

      <div className="knowledge-square-domain-tabs" aria-label={t("knowledge.domainFilter")}>
        {domains.map((item) => (
          <button
            key={item}
            className={domain === item ? "is-active" : ""}
            type="button"
            onClick={() => setDomain(item)}
          >
            {item === "全部" ? t("common.all") : item}
          </button>
        ))}
      </div>

      <div className="knowledge-square-grid">
        {visibleItems.map((item) => {
          const itemStatus = statusMap[item.id] || {
            installed: item.installed,
            updateAvailable: Boolean(item.updateAvailable),
          };
          return (
            <article
              key={item.id}
              className={`knowledge-square-card ${itemStatus.installed ? "is-installed" : ""} ${itemStatus.updateAvailable ? "is-update" : ""}`}
              tabIndex={0}
              role="button"
              aria-label={t("knowledge.viewSquareDetail", { name: item.name })}
              data-icon={item.icon}
              onClick={() => setDetailItem(item)}
              onKeyDown={(event) => {
                if (event.key === "Enter" || event.key === " ") {
                  event.preventDefault();
                  setDetailItem(item);
                }
              }}
            >
              <span className="knowledge-square-card-icon" aria-hidden="true">
                {iconByType[item.icon] || <DatabaseOutlined />}
              </span>
              <div className="knowledge-square-card-heading">
                <div className="knowledge-square-card-title-line">
                  <strong>{item.name}</strong>
                  {itemStatus.installed ? (
                    <span className="knowledge-square-installed-label">
                      {t("knowledge.installed")}
                    </span>
                  ) : null}
                </div>
                <span>{item.domain}</span>
              </div>
              <p>{item.desc}</p>
              <div className="knowledge-square-card-tags">
                {item.tags.slice(0, 1).map((tag) => (
                  <span key={tag}>{tag}</span>
                ))}
              </div>
              <div className="knowledge-square-card-meta">
                <span><FileTextOutlined />{t("knowledge.documentCount", { count: item.docs })}</span>
                <span><ClockCircleOutlined />{item.updated.slice(5)} {t("knowledge.updatedShort")}</span>
              </div>
              <div className="knowledge-square-card-actions">
                <span className={itemStatus.updateAvailable ? "is-update" : "is-ready"}>
                  <i />
                  {itemStatus.updateAvailable ? t("knowledge.updateAvailable") : item.version}
                </span>
                <div>
                  <Button
                    size="small"
                    onClick={(event: MouseEvent<HTMLElement>) => {
                      event.stopPropagation();
                      onQuery(item);
                    }}
                  >
                    {t("knowledge.onlineQuery")}
                  </Button>
                  <Button
                    size="small"
                    type="primary"
                    onClick={(event: MouseEvent<HTMLElement>) => {
                      event.stopPropagation();
                      if (itemStatus.updateAvailable) {
                        onUpdate(item);
                      } else if (itemStatus.installed) {
                        onOpen(item);
                      } else {
                        onInstall(item);
                      }
                    }}
                  >
                    {itemStatus.updateAvailable
                      ? t("common.update")
                      : itemStatus.installed
                        ? t("common.open")
                        : t("common.install")}
                  </Button>
                </div>
              </div>
            </article>
          );
        })}
        {visibleItems.length === 0 ? (
          <div className="knowledge-square-empty">
            <DatabaseOutlined />
            <strong>{t("knowledge.squareEmptyTitle")}</strong>
            <span>{t("knowledge.squareEmptyDescription")}</span>
          </div>
        ) : null}
      </div>

      <Modal
        className="knowledge-square-detail-modal"
        width={680}
        open={Boolean(detailItem)}
        title={detailItem?.name}
        centered
        footer={
          detailItem ? (
            <>
              <Button onClick={() => onQuery(detailItem)}>{t("knowledge.onlineQuery")}</Button>
              <Button
                type="primary"
                onClick={() => {
                  const itemStatus = statusMap[detailItem.id];
                  if (itemStatus?.updateAvailable) {
                    onUpdate(detailItem);
                  } else if (itemStatus?.installed) {
                    onOpen(detailItem);
                  } else {
                    onInstall(detailItem);
                  }
                  setDetailItem(null);
                }}
              >
                {statusMap[detailItem.id]?.updateAvailable
                  ? t("common.update")
                  : statusMap[detailItem.id]?.installed
                    ? t("common.open")
                    : t("common.install")}
              </Button>
            </>
          ) : null
        }
        onCancel={() => setDetailItem(null)}
      >
        {detailItem ? (
          <div className="knowledge-square-detail-content">
            <p>{detailItem.desc}</p>
            <dl>
              <div><dt>{t("knowledge.coverage")}</dt><dd>{detailItem.coverage}</dd></div>
              <div><dt>{t("knowledge.source")}</dt><dd>{detailItem.source}</dd></div>
              <div><dt>{t("knowledge.documentCountLabel")}</dt><dd>{detailItem.docs}</dd></div>
              <div><dt>{t("knowledge.parseSize")}</dt><dd>{detailItem.size}</dd></div>
              <div><dt>{t("knowledge.version")}</dt><dd>{detailItem.version}</dd></div>
              <div><dt>{t("knowledge.updateDate")}</dt><dd>{detailItem.updated}</dd></div>
            </dl>
            <div className="knowledge-square-detail-questions">
              <strong>{t("knowledge.exampleQuestions")}</strong>
              {detailItem.questions.map((question) => <span key={question}>{question}</span>)}
            </div>
          </div>
        ) : null}
      </Modal>
    </div>
  );
}
