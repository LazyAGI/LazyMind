import { BookOutlined, ReadOutlined, EditOutlined, TranslationOutlined, SafetyCertificateOutlined, CodeOutlined, HistoryOutlined, SlidersOutlined, BankOutlined, ExperimentOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import "./index.scss";

function PaperCollectionIcon() {
  return <svg className="paper-collection-mark" width="24" height="24" viewBox="0 0 24 24" fill="none" aria-hidden="true">
    <path d="M6 3h11a2 2 0 0 1 2 2v13" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" opacity=".45" />
    <path d="M4 6h10l3 3v11a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1Z" fill="currentColor" />
    <path d="M13 6v4h4" fill="#d8b4ef" />
    <path d="M6.5 12h7M6.5 15h7M6.5 18h4" stroke="white" strokeWidth="1.35" strokeLinecap="round" />
  </svg>;
}

const types = {
  general: { Icon: BookOutlined, label: "learning.profile.general.name", tone: "blue" },
  academic_papers: { Icon: PaperCollectionIcon, label: "learning.profile.academicPapers.name", tone: "paper" },
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
