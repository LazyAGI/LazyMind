export type KnowledgeSquareType = "industry" | "evaluation";

export interface OfficialKnowledgeBase {
  id: string;
  type: KnowledgeSquareType;
  domain: string;
  icon: string;
  name: string;
  desc: string;
  tags: string[];
  docs: number;
  size: string;
  version: string;
  updated: string;
  installed: boolean;
  updateAvailable?: boolean;
  coverage: string;
  source: string;
  questions: string[];
}

export const OFFICIAL_KNOWLEDGE_BASES: OfficialKnowledgeBase[] = [
  {
    id: "law-cn",
    type: "industry",
    domain: "法律",
    icon: "law",
    name: "中国法律法规知识库",
    desc: "收录现行有效的法律、行政法规与常用司法解释，支持法规查找和条款溯源。",
    tags: ["法律法规", "司法解释"],
    docs: 438,
    size: "286 MB",
    version: "v2.3.0",
    updated: "2026-07-18",
    installed: true,
    updateAvailable: true,
    coverage: "国家级法律法规",
    source: "国家法律法规数据库及官方公开文件",
    questions: ["劳动合同在什么情况下可以解除？", "民法典关于个人信息保护有哪些规定？"],
  },
  {
    id: "finance",
    type: "industry",
    domain: "金融",
    icon: "finance",
    name: "金融监管与业务知识库",
    desc: "覆盖银行、证券、基金与保险领域的基础制度、监管规则和常见业务术语。",
    tags: ["监管规则", "金融业务"],
    docs: 326,
    size: "214 MB",
    version: "v1.8.0",
    updated: "2026-07-10",
    installed: true,
    updateAvailable: true,
    coverage: "银行、证券、基金、保险",
    source: "金融监管机构公开文件",
    questions: ["商业银行资本充足率有哪些要求？", "什么是合格投资者制度？"],
  },
  {
    id: "government",
    type: "industry",
    domain: "政务",
    icon: "government",
    name: "政务服务事项知识库",
    desc: "汇总高频政务服务事项、办事指南、申报材料及常见问题，适用于政务咨询。",
    tags: ["办事指南", "公共服务"],
    docs: 512,
    size: "304 MB",
    version: "v1.5.2",
    updated: "2026-07-12",
    installed: false,
    coverage: "高频公共服务事项",
    source: "政府门户网站与政务服务公开指南",
    questions: ["企业设立登记需要哪些材料？", "社保关系转移怎么办理？"],
  },
  {
    id: "medical",
    type: "industry",
    domain: "医疗",
    icon: "medical",
    name: "临床医学基础知识库",
    desc: "覆盖常见疾病、检查指标、诊疗指南与用药基础知识，仅用于专业知识辅助查询。",
    tags: ["诊疗指南", "医学基础"],
    docs: 287,
    size: "258 MB",
    version: "v2.0.1",
    updated: "2026-07-20",
    installed: false,
    coverage: "常见疾病与临床指南",
    source: "权威医学指南及公开专业资料",
    questions: ["高血压的常用分级标准是什么？", "糖尿病患者日常管理要注意什么？"],
  },
  {
    id: "education",
    type: "industry",
    domain: "教育",
    icon: "education",
    name: "基础教育教学知识库",
    desc: "整理课程标准、教学设计方法、教育评价与课堂管理资料，辅助备课和教学研究。",
    tags: ["课程标准", "教学设计"],
    docs: 196,
    size: "146 MB",
    version: "v1.4.0",
    updated: "2026-06-28",
    installed: false,
    coverage: "基础教育与通用教学法",
    source: "课程标准及公开教育研究资料",
    questions: ["如何设计一节探究式课堂？", "形成性评价有哪些常用方法？"],
  },
  {
    id: "internet",
    type: "industry",
    domain: "互联网",
    icon: "internet",
    name: "互联网技术实践知识库",
    desc: "覆盖云原生、前后端开发、数据库、网络与工程实践，适合技术问答和研发辅助。",
    tags: ["云原生", "软件工程"],
    docs: 362,
    size: "276 MB",
    version: "v3.1.0",
    updated: "2026-07-22",
    installed: true,
    coverage: "通用互联网研发技术",
    source: "官方技术文档及精选工程实践",
    questions: ["微服务系统如何设计限流策略？", "数据库索引失效的常见原因有哪些？"],
  },
  {
    id: "human-resources",
    type: "industry",
    domain: "人力资源",
    icon: "people",
    name: "人力资源管理知识库",
    desc: "覆盖招聘配置、绩效管理、薪酬福利、人才发展与员工关系等常用人力资源制度。",
    tags: ["人才管理", "员工关系"],
    docs: 248,
    size: "172 MB",
    version: "v1.2.0",
    updated: "2026-07-16",
    installed: false,
    coverage: "通用人力资源管理制度与实践",
    source: "公开人力资源规范及精选管理实践",
    questions: ["如何设计结构化面试流程？", "绩效目标应该如何制定？"],
  },
  {
    id: "manufacturing",
    type: "industry",
    domain: "制造",
    icon: "manufacturing",
    name: "智能制造与质量管理知识库",
    desc: "整理生产计划、设备管理、质量体系、精益生产与智能制造相关标准和实践。",
    tags: ["质量管理", "智能制造"],
    docs: 394,
    size: "268 MB",
    version: "v2.1.0",
    updated: "2026-07-24",
    installed: true,
    coverage: "离散制造、流程制造与通用质量体系",
    source: "公开行业标准及制造业实践资料",
    questions: ["如何建立生产异常闭环机制？", "精益生产常用工具有哪些？"],
  },
  {
    id: "retail",
    type: "industry",
    domain: "零售",
    icon: "retail",
    name: "零售运营与消费者洞察知识库",
    desc: "覆盖商品、门店、会员、营销、供应链及消费者洞察等零售运营核心内容。",
    tags: ["零售运营", "消费者洞察"],
    docs: 231,
    size: "158 MB",
    version: "v1.3.1",
    updated: "2026-07-19",
    installed: false,
    coverage: "线上线下一体化零售运营",
    source: "公开行业报告及精选运营资料",
    questions: ["如何设计会员分层体系？", "门店经营分析应关注哪些指标？"],
  },
  {
    id: "new-energy",
    type: "industry",
    domain: "能源",
    icon: "energy",
    name: "新能源产业知识库",
    desc: "聚合光伏、风电、储能、新能源汽车及能源安全相关的产业政策和技术资料。",
    tags: ["新能源", "产业政策"],
    docs: 318,
    size: "224 MB",
    version: "v1.6.0",
    updated: "2026-07-21",
    installed: false,
    coverage: "新能源产业政策、技术与安全规范",
    source: "主管部门公开文件及行业技术资料",
    questions: ["储能项目需要关注哪些安全要求？", "光伏产业链包含哪些主要环节？"],
  },
  {
    id: "hotpotqa",
    type: "evaluation",
    domain: "多跳问答",
    icon: "evaluation",
    name: "HotpotQA 评测知识集",
    desc: "面向多跳推理与证据组合能力评测的问答知识集。",
    tags: ["多跳推理", "英文"],
    docs: 113,
    size: "168 MB",
    version: "v1.1",
    updated: "2026-07-01",
    installed: false,
    coverage: "多文档问答",
    source: "公开评测数据整理",
    questions: ["查看一条多跳问答样例", "这个测试集主要评估什么能力？"],
  },
  {
    id: "nq",
    type: "evaluation",
    domain: "开放问答",
    icon: "evaluation",
    name: "Natural Questions 评测知识集",
    desc: "用于评测开放域问答和长文档证据定位能力。",
    tags: ["开放问答", "英文"],
    docs: 87,
    size: "231 MB",
    version: "v1.0",
    updated: "2026-06-26",
    installed: false,
    coverage: "开放域事实问答",
    source: "公开评测数据整理",
    questions: ["给我一个开放问答样例", "适合评测哪些 RAG 指标？"],
  },
  {
    id: "triviaqa",
    type: "evaluation",
    domain: "事实问答",
    icon: "evaluation",
    name: "TriviaQA 评测知识集",
    desc: "覆盖大规模事实性问答，适合测试检索召回和答案准确率。",
    tags: ["事实问答", "英文"],
    docs: 102,
    size: "206 MB",
    version: "v1.0",
    updated: "2026-06-24",
    installed: false,
    coverage: "事实性知识问答",
    source: "公开评测数据整理",
    questions: ["展示一个事实问答样例", "如何计算答案准确率？"],
  },
  {
    id: "musique",
    type: "evaluation",
    domain: "复杂推理",
    icon: "evaluation",
    name: "MuSiQue 评测知识集",
    desc: "用于评估可组合多跳推理，强调问题分解和证据链。",
    tags: ["复杂推理", "证据链"],
    docs: 76,
    size: "139 MB",
    version: "v1.2",
    updated: "2026-07-03",
    installed: false,
    coverage: "组合式多跳问答",
    source: "公开评测数据整理",
    questions: ["展示一个证据链样例", "什么是可组合多跳推理？"],
  },
  {
    id: "financebench",
    type: "evaluation",
    domain: "金融问答",
    icon: "evaluation",
    name: "FinanceBench 评测知识集",
    desc: "面向金融文档问答，考察报告检索、数值理解与证据引用。",
    tags: ["金融", "长文档"],
    docs: 164,
    size: "201 MB",
    version: "v1.1",
    updated: "2026-07-05",
    installed: false,
    coverage: "金融报告问答",
    source: "公开评测数据整理",
    questions: ["展示一条财报问答样例", "是否包含数值推理问题？"],
  },
  {
    id: "pubmedqa",
    type: "evaluation",
    domain: "医学问答",
    icon: "evaluation",
    name: "PubMedQA 评测知识集",
    desc: "基于生物医学论文摘要的问题回答评测集。",
    tags: ["医学", "论文问答"],
    docs: 211,
    size: "248 MB",
    version: "v1.0",
    updated: "2026-07-07",
    installed: false,
    coverage: "生物医学论文问答",
    source: "公开评测数据整理",
    questions: ["展示一个医学论文问答样例", "答案标签有哪些？"],
  },
  {
    id: "lawbench",
    type: "evaluation",
    domain: "法律问答",
    icon: "evaluation",
    name: "LawBench 评测知识集",
    desc: "面向法律知识理解、条款检索和案例推理能力评测。",
    tags: ["法律", "中文"],
    docs: 138,
    size: "174 MB",
    version: "v1.3",
    updated: "2026-07-11",
    installed: false,
    coverage: "中文法律能力评测",
    source: "公开评测数据整理",
    questions: ["展示一个法律推理样例", "包含哪些法律任务？"],
  },
  {
    id: "cmrc",
    type: "evaluation",
    domain: "阅读理解",
    icon: "evaluation",
    name: "CMRC 2018 评测知识集",
    desc: "中文机器阅读理解数据集，用于评测篇章检索和答案抽取。",
    tags: ["中文", "阅读理解"],
    docs: 64,
    size: "96 MB",
    version: "v1.0",
    updated: "2026-06-29",
    installed: false,
    coverage: "中文抽取式问答",
    source: "公开评测数据整理",
    questions: ["展示一个中文阅读理解样例", "推荐使用哪些评测指标？"],
  },
  {
    id: "longbench",
    type: "evaluation",
    domain: "长文本",
    icon: "evaluation",
    name: "LongBench-RAG 评测知识集",
    desc: "用于评估长文本条件下的检索、理解和跨段信息整合能力。",
    tags: ["长文本", "综合评测"],
    docs: 129,
    size: "293 MB",
    version: "v1.2",
    updated: "2026-07-14",
    installed: false,
    coverage: "长上下文与 RAG",
    source: "公开评测数据整理",
    questions: ["展示一个长文本问答样例", "这个测试集覆盖哪些任务？"],
  },
];

export interface KnowledgeSquareStatus {
  installed: boolean;
  updateAvailable: boolean;
}

export type KnowledgeSquareStatusMap = Record<string, KnowledgeSquareStatus>;

export function createInitialKnowledgeSquareStatus(): KnowledgeSquareStatusMap {
  return Object.fromEntries(
    OFFICIAL_KNOWLEDGE_BASES.map((item) => [
      item.id,
      {
        installed: item.installed,
        updateAvailable: Boolean(item.updateAvailable),
      },
    ]),
  );
}

export function filterOfficialKnowledgeBases({
  items,
  type,
  domain,
  status,
  keyword,
  statusMap,
}: {
  items: OfficialKnowledgeBase[];
  type: KnowledgeSquareType;
  domain: string;
  status: "all" | "installed" | "uninstalled" | "update";
  keyword: string;
  statusMap: KnowledgeSquareStatusMap;
}) {
  const normalizedKeyword = keyword.trim().toLocaleLowerCase();

  return items.filter((item) => {
    const itemStatus = statusMap[item.id] || {
      installed: item.installed,
      updateAvailable: Boolean(item.updateAvailable),
    };
    const matchesStatus =
      status === "all" ||
      (status === "installed" && itemStatus.installed) ||
      (status === "uninstalled" && !itemStatus.installed) ||
      (status === "update" && itemStatus.updateAvailable);
    const haystack = [item.name, item.desc, item.domain, ...item.tags]
      .join(" ")
      .toLocaleLowerCase();

    return (
      item.type === type &&
      (domain === "全部" || item.domain === domain) &&
      matchesStatus &&
      (!normalizedKeyword || haystack.includes(normalizedKeyword))
    );
  });
}
