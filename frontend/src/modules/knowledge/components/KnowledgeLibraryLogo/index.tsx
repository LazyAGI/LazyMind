import { BookOutlined, FileSearchOutlined, ReadOutlined, EditOutlined, TranslationOutlined, SafetyCertificateOutlined, CodeOutlined, HistoryOutlined, SlidersOutlined, BankOutlined, ExperimentOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import "./index.scss";

const types = {
  general: { Icon: BookOutlined, label: "learning.profile.general.name", tone: "blue" },
  academic_papers: { Icon: FileSearchOutlined, label: "learning.profile.academicPapers.name", tone: "indigo" },
  chinese_modern: { Icon: ReadOutlined, label: "learning.profile.chineseModern.name", tone: "green" },
  chinese_classical: { Icon: EditOutlined, label: "learning.profile.chineseClassical.name", tone: "brown" },
  english_learning: { Icon: TranslationOutlined, label: "learning.profile.englishLearning.name", tone: "teal" },
  legal: { Icon: SafetyCertificateOutlined, label: "learning.profile.legal.name", tone: "navy" },
  technical: { Icon: CodeOutlined, label: "learning.profile.technical.name", tone: "cyan" },
  historical: { Icon: HistoryOutlined, label: "learning.profile.historical.name", tone: "amber" },
  custom: { Icon: SlidersOutlined, label: "learning.customProfile", tone: "gray" },
  official_industry: { Icon: BankOutlined, label: "knowledge.officialKnowledge", tone: "navy" },
  official_evaluation: { Icon: ExperimentOutlined, label: "knowledge.officialKnowledge", tone: "indigo" },
} as const;

export default function KnowledgeLibraryLogo({ scene, compact = false }: { scene?: string; compact?: boolean }) {
  const { t } = useTranslation();
  const key = scene || "general";
  const { Icon, label, tone } = types[key as keyof typeof types] || types.custom;
  return <span className={`knowledge-library-logo tone-${tone}${compact ? " is-compact" : ""}`}
    role="img" aria-label={t(label)} title={t(label)}><Icon aria-hidden /></span>;
}
