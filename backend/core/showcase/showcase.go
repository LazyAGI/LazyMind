package showcase

import (
	"encoding/json"
	"net/http"
	"strings"

	"lazymind/core/common"
)

// ShowcaseCaseStep is one user-visible step in a case replay.
type ShowcaseCaseStep struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// ShowcaseCaseTask is a selectable task shown on a case detail page.
type ShowcaseCaseTask struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	OutputLabel string `json:"output_label,omitempty"`
	Prompt      string `json:"prompt,omitempty"`
}

// ShowcaseCaseOption is an optional second-level category shown in the chat composer.
type ShowcaseCaseOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Prompt      string `json:"prompt,omitempty"`
}

// ShowcaseCase is a built-in, read-only case that helps users start a real chat.
type ShowcaseCase struct {
	ID               string               `json:"id"`
	Title            string               `json:"title"`
	Description      string               `json:"description"`
	Category         string               `json:"category"`
	PrimaryCategory  string               `json:"primary_category,omitempty"`
	SecondaryOptions []ShowcaseCaseOption `json:"secondary_options,omitempty"`
	OutputType       string               `json:"output_type"`
	OutputLabel      string               `json:"output_label"`
	ImageURL         string               `json:"image_url"`
	AttachmentHint   string               `json:"attachment_hint,omitempty"`
	PromptShort      string               `json:"prompt_short"`
	Prompt           string               `json:"prompt"`
	ResultSummary    string               `json:"result_summary"`
	ResultHighlights []string             `json:"result_highlights"`
	Steps            []ShowcaseCaseStep   `json:"steps"`
	Tasks            []ShowcaseCaseTask   `json:"tasks,omitempty"`
}

// ShowcaseCaseListResponse is the directory response used by the homepage and gallery.
type ShowcaseCaseListResponse struct {
	Cases      []ShowcaseCase `json:"cases"`
	Categories []string       `json:"categories"`
	Total      int            `json:"total"`
}

var categories = []string{
	"全部",
	"调研分析",
	"数据分析",
	"PPT 制作",
	"文档写作",
	"内容创作",
	"图片设计",
	"网页制作",
	"办公效率",
}

var showcaseCases = []ShowcaseCase{
	{
		ID: "aiProduct", Title: "产品设计与 PRD 生成", Description: "从用户需求到任务流程，输出完整的 Agent 产品设计方案", Category: "文档写作", PrimaryCategory: "产品设计与PRD生成", SecondaryOptions: []ShowcaseCaseOption{
			{ID: "full", Label: "完整功能", Description: "覆盖完整任务链路和交付标准", Prompt: "请完整覆盖任务链路和交付标准，输出可直接评审的完整方案。"},
			{ID: "outline", Label: "快速生成", Description: "先产出结构化初稿", Prompt: "请优先产出结构化初稿，先确认核心结论和执行框架，再逐步补充细节。"},
		}, OutputType: "web", OutputLabel: "产品设计方案", ImageURL: "/showcase/15-ai-agent-product.png", AttachmentHint: "AI Agent 产品需求说明.docx",
		PromptShort: "根据产品需求设计一款能够连续执行复杂任务的 AI Agent 产品。", Prompt: "请根据我上传的产品需求说明设计一款面向知识工作者的 AI Agent 产品，完成目标用户、核心场景、价值主张、任务流程、信息架构、关键交互、异常处理、信任机制和核心指标设计，并输出可用于产品评审的完整方案。", ResultSummary: "把用户目标拆解成可执行、可追踪、可交付的 Agent 产品方案。", ResultHighlights: []string{"目标用户与核心场景", "任务流程与信息架构", "关键交互、异常处理与指标体系"},
		Steps: []ShowcaseCaseStep{
			{Title: "解析产品需求", Description: "识别目标用户、核心问题与业务约束"},
			{Title: "梳理任务场景", Description: "提炼高频任务、用户旅程和关键触点"},
			{Title: "设计产品结构", Description: "规划信息架构、任务流程与核心交互"},
			{Title: "补充信任机制", Description: "设计计划确认、过程控制与异常处理"},
			{Title: "输出设计方案", Description: "整理产品说明、指标体系和评审重点"},
		}, Tasks: []ShowcaseCaseTask{
			{ID: "product-design", Title: "产品设计", Description: "从目标用户与核心场景出发，完成产品结构、任务流程和关键交互设计。", OutputLabel: "产品设计方案", Prompt: "请根据我上传的产品需求说明完成 AI Agent 产品设计，重点输出目标用户、核心场景、任务流程、信息架构和关键交互。"},
			{ID: "competitor-analysis", Title: "竞品分析", Description: "研究代表性竞品的定位、能力与策略，识别市场机会和差异化方向。", OutputLabel: "竞品调研报告", Prompt: "请根据我上传的产品需求和竞品资料完成竞品分析，比较代表性产品的定位、核心能力、目标用户和差异化机会。"},
			{ID: "product-comparison", Title: "产品对比报告", Description: "按统一维度对多个产品进行横向对比，输出清晰的选型建议。", OutputLabel: "产品对比报告", Prompt: "请根据我上传的竞品资料生成产品对比报告，按照统一维度比较产品能力、适用场景、成本和差异化优势，并给出选型建议。"},
			{ID: "prd-generation", Title: "PRD 生成", Description: "把产品构想整理成可评审、可开发、包含验收标准的完整需求文档。", OutputLabel: "产品需求文档", Prompt: "请根据我上传的产品需求和前面的分析结果生成完整 PRD，包含功能需求、关键交互、异常处理、验收标准、指标体系和版本规划。"},
		}},
	{
		ID: "knowledgeQa", Title: "知识库智能问答解决方案", Description: "规划知识接入、检索问答与权限治理，形成落地方案", Category: "文档写作", PrimaryCategory: "知识库问答", OutputType: "report", OutputLabel: "解决方案", ImageURL: "/showcase/16-knowledge-qa.png", AttachmentHint: "知识库资料与需求说明.zip",
		PromptShort: "根据企业知识资料和业务需求，设计一套可追溯的知识库智能问答解决方案。", Prompt: "请根据我上传的知识库资料和业务需求，设计一套企业知识库智能问答解决方案，覆盖数据接入与清洗、文档解析、知识切分、混合检索、答案生成、来源引用、权限隔离、效果评测、运营闭环和分阶段实施计划。", ResultSummary: "从资料接入到答案引用，形成可落地、可追溯的企业知识库方案。", ResultHighlights: []string{"数据接入与文档解析", "混合检索、引用和权限隔离", "效果评测与运营闭环"},
		Steps: steps("盘点知识来源", "识别文档类型、权限和更新频率")},
	{
		ID: "industry", Title: "市场调研与竞品分析", Description: "分析市场规模、消费趋势与品牌格局，生成完整报告", Category: "调研分析", OutputType: "report", OutputLabel: "网页报告", ImageURL: "/showcase/01-pet-market.png", AttachmentHint: "宠物用品行业资料.pdf",
		PromptShort: "调研国内宠物用品市场，分析市场规模、消费趋势、主要品牌和未来机会。", Prompt: "请结合我提供的资料调研国内宠物用品市场，重点分析市场规模、消费人群、品类趋势、主要品牌、竞争格局和未来机会，并生成一份结构完整、带信息来源的市场调研报告。", ResultSummary: "围绕市场规模、用户趋势和竞争格局，输出带来源的调研判断。", ResultHighlights: []string{"市场规模与消费人群", "品类趋势与竞争格局", "市场机会与进入建议"},
		Steps: steps("定义研究问题", "明确市场范围、用户和判断口径")},
	{
		ID: "sales", Title: "数据提取与分析", Description: "定位区域销售下滑原因，生成指标看板与改善建议", Category: "数据分析", OutputType: "dashboard", OutputLabel: "数据看板", ImageURL: "/showcase/02-sales-analysis.png", AttachmentHint: "华东区销售数据.xlsx",
		PromptShort: "分析华东区销售数据，定位销售额下滑的关键原因并提出改善建议。", Prompt: "请分析我上传的华东区销售数据，比较各城市、产品和渠道的销售额、订单量、客单价及同比变化，定位销售下滑的关键原因，并生成包含图表、结论和改善建议的诊断报告。", ResultSummary: "把零散销售数据转成可解释的指标变化、原因判断与行动建议。", ResultHighlights: []string{"城市、产品和渠道拆解", "销售额、订单量与客单价对比", "原因定位和改善建议"},
		Steps: steps("读取数据结构", "识别字段、时间范围和缺失值")},
	{
		ID: "ppt", Title: "个人年度总结 PPT 生成", Description: "汇总年度经历、成果与成长，生成结构完整的个人报告", Category: "PPT 制作", OutputType: "slides", OutputLabel: "PPT 预览", ImageURL: "/showcase/03-annual-report.png", AttachmentHint: "个人年度记录.docx",
		PromptShort: "根据我的年度记录制作一份个人年度报告 PPT，呈现经历、成果、成长和未来计划。", Prompt: "请根据我上传的个人年度记录制作一份个人年度报告 PPT，围绕年度关键词、重要经历、关键成果、能力成长、难忘时刻、复盘思考和下一年度计划建立叙事主线，并采用简洁、有温度且具有个人表达的视觉风格。", ResultSummary: "将年度记录整理成有叙事主线、可展示、可继续编辑的汇报材料。", ResultHighlights: []string{"年度关键词和叙事主线", "成果、成长与复盘", "下一年度计划"},
		Steps: steps("收集年度记录", "提取经历、成果和成长线索")},
	{
		ID: "proposal", Title: "小红书探店种草文案", Description: "提炼门店亮点与消费体验，生成自然有吸引力的种草文案", Category: "内容创作", OutputType: "document", OutputLabel: "探店种草文案", ImageURL: "/showcase/04-xiaohongshu-copy.png", AttachmentHint: "探店照片与门店信息.zip",
		PromptShort: "根据探店照片和门店信息，写一篇真实自然、有吸引力的小红书探店种草文案。", Prompt: "请根据我上传的探店照片和门店信息，提炼环境氛围、招牌产品、真实体验、价格与交通等实用信息，写一篇口吻自然、重点突出且适合发布在小红书的探店种草文案，并补充标题和相关话题标签。", ResultSummary: "保留真实体验和实用信息，形成适合发布的内容初稿。", ResultHighlights: []string{"多组标题与开场", "环境、招牌产品和真实体验", "话题标签与配图建议"},
		Steps: steps("识别门店信息", "提炼环境、产品、价格和交通")},
	{
		ID: "stickers", Title: "创意图片与表情包生成", Description: "覆盖图片生成、局部编辑与表情包制作，快速完成多种视觉任务", Category: "图片设计", OutputType: "images", OutputLabel: "图片候选方案", ImageURL: "/showcase/05-stickers.png", AttachmentHint: "视觉需求与参考图.zip",
		PromptShort: "根据视觉需求和参考图，生成一组主题一致、风格明确的高质量图片。", Prompt: "请根据我上传的视觉需求和参考图生成一组高质量图片，准确呈现主体、场景、构图、光线、色彩和风格要求，并提供适合不同使用场景的候选方案。", ResultSummary: "统一主体、风格和镜头语言，产出一组可比较的视觉候选方案。", ResultHighlights: []string{"主体、场景和构图分析", "风格、光线和色彩方案", "多场景候选图片"},
		Steps: steps("解析视觉需求", "提取主体、风格、构图和用途")},
	{
		ID: "landing", Title: "智能手表产品手册", Description: "整理产品参数、功能卖点和场景，输出可交互数字手册", Category: "网页制作", OutputType: "web", OutputLabel: "网页手册", ImageURL: "/showcase/06-smartwatch-manual.png", AttachmentHint: "智能手表产品资料.docx",
		PromptShort: "根据产品资料制作一份面向消费者的智能手表数字产品手册。", Prompt: "请根据我上传的智能手表产品资料设计并制作一份面向消费者的可交互数字产品手册，清晰呈现外观设计、健康监测、运动模式、续航能力、兼容性、核心参数和典型使用场景，并兼顾桌面端与移动端。", ResultSummary: "把产品参数和使用场景组织成适合消费者浏览的数字手册。", ResultHighlights: []string{"首屏卖点与产品故事", "功能、参数和使用场景", "桌面端与移动端适配"},
		Steps: steps("整理产品资料", "提取卖点、参数和典型场景")},
	{
		ID: "meeting", Title: "每日定时资讯简报", Description: "每天定时汇总 AI 行业动态，生成精炼资讯简报", Category: "办公效率", OutputType: "meeting", OutputLabel: "定时资讯简报", ImageURL: "/showcase/08-daily-briefing.png",
		PromptShort: "创建一个每天上午九点自动汇总并推送 AI 行业资讯的定时任务。", Prompt: "请创建一个每天上午九点执行的定时任务，汇总过去二十四小时内重要的 AI 产品发布、模型更新、行业融资和政策动态，筛选最值得关注的内容并生成一份附带来源链接的精炼资讯简报。", ResultSummary: "将资讯范围、执行时间、筛选规则和推送目标整理成可确认的任务草案。", ResultHighlights: []string{"每日 09:00 执行", "产品、模型、融资和政策动态", "去重、核验并附来源链接"},
		Steps: steps("定义资讯范围", "确认关注主题和时间窗口")},
	{
		ID: "competitor", Title: "国内 AI 办公助手对比", Description: "比较主流产品的功能、定位、价格与差异化能力", Category: "调研分析", OutputType: "table", OutputLabel: "对比报告", ImageURL: "/showcase/07-office-assistants.png", AttachmentHint: "AI 办公助手清单.xlsx",
		PromptShort: "调研国内主流 AI 办公助手，从功能、价格和目标用户等维度生成对比分析。", Prompt: "请调研国内主流 AI 办公助手，从产品定位、目标用户、核心功能、文档与数据处理能力、定价、商业模式和市场传播等维度进行比较，输出带来源的竞品对比表并总结各产品的差异化优势。", ResultSummary: "用统一维度比较产品能力和商业模式，给出清晰的选型依据。", ResultHighlights: []string{"定位与目标用户", "功能、文档和数据能力", "价格、模式和差异化优势"},
		Steps: steps("确定竞品范围", "筛选直接竞品和替代方案")},
	{
		ID: "policy", Title: "追踪政策变化并提炼影响", Description: "汇总权威政策信息，提炼企业需要关注的变化", Category: "调研分析", OutputType: "report", OutputLabel: "政策简报", ImageURL: "/showcase/09-policy-tracking.png", AttachmentHint: "示例政策清单.pdf",
		PromptShort: "追踪目标领域近期政策变化，并评估对业务、合规和机会的影响。", Prompt: "请调研【政策主题】近期的重要政策变化，优先使用政府及权威机构来源，并说明政策背景、主要变化、影响对象、生效时间以及对【目标业务】的机会、风险和行动建议。", ResultSummary: "将政策原文和生效节点转化为业务可执行的风险与机会提示。", ResultHighlights: []string{"权威来源与政策背景", "变化内容、影响对象和生效时间", "业务机会、风险与行动建议"},
		Steps: steps("确认追踪主题", "界定政策范围、业务和影响对象")},
	{
		ID: "feedback", Title: "归纳用户反馈与产品机会", Description: "从大量反馈中聚类问题，识别高价值产品机会", Category: "数据分析", OutputType: "dashboard", OutputLabel: "洞察看板", ImageURL: "/showcase/10-user-feedback.png", AttachmentHint: "示例用户反馈.csv",
		PromptShort: "分析用户反馈，归纳高频问题、情绪变化和最值得优先解决的产品机会。", Prompt: "请分析我上传的【用户反馈数据】，完成主题聚类、情绪分析和问题频次统计，识别高影响、高频率的问题，并按用户价值、业务价值和实现成本给出产品机会优先级。", ResultSummary: "从反馈主题、情绪和频次出发，形成可排序的产品机会清单。", ResultHighlights: []string{"主题聚类与问题频次", "情绪变化与高影响问题", "按价值和成本排序的机会"},
		Steps: steps("清洗反馈数据", "去重并识别有效反馈")},
	{
		ID: "article", Title: "把访谈素材整理成深度文章", Description: "提炼观点与故事线，生成可发布的内容初稿", Category: "内容创作", OutputType: "document", OutputLabel: "文章初稿", ImageURL: "/showcase/11-interview-article.png", AttachmentHint: "示例访谈记录.docx",
		PromptShort: "根据访谈记录提炼核心观点，整理成一篇结构清晰、可读性强的深度文章。", Prompt: "请根据我上传的【访谈记录】撰写一篇面向【目标读者】的深度文章，保留受访者的重要观点与代表性表达，建立清晰的故事线，并补充小标题、开头导语和结尾总结。", ResultSummary: "保留访谈中的关键表达，同时建立面向目标读者的文章结构。", ResultHighlights: []string{"核心观点与代表性表达", "导语、故事线和小标题", "可继续编辑的文章初稿"},
		Steps: steps("整理访谈素材", "识别人物、观点、事实和表达")},
	{
		ID: "weekly", Title: "汇总工作记录生成周报", Description: "从零散记录中整理成果、问题和下周计划", Category: "办公效率", OutputType: "meeting", OutputLabel: "结构化周报", ImageURL: "/showcase/12-weekly-report.png", AttachmentHint: "示例工作记录.txt",
		PromptShort: "把本周零散工作记录整理成重点突出、适合向上汇报的周报。", Prompt: "请把我提供的【本周工作记录】整理成一份重点突出、适合向上汇报的周报，覆盖本周成果、关键进展、问题与风险、需要协助的事项和下周计划，并突出可量化结果。", ResultSummary: "从零散记录中提炼成果、风险和行动计划，形成可汇报周报。", ResultHighlights: []string{"本周成果与关键进展", "问题、风险和协作诉求", "下周计划与量化结果"},
		Steps: steps("汇总工作记录", "按项目归类任务、会议和进展")},
	{
		ID: "paper", Title: "学术论文写作", Description: "整合研究资料与参考文献，生成结构规范的学术论文初稿", Category: "文档写作", OutputType: "document", OutputLabel: "论文初稿", ImageURL: "/showcase/13-ai-education-paper.png", AttachmentHint: "研究资料与参考文献.zip",
		PromptShort: "根据研究资料和参考文献，撰写一篇关于生成式 AI 教育应用的学术论文初稿。", Prompt: "请根据我上传的研究资料和参考文献，围绕生成式 AI 支持个性化学习的作用机制与实践路径撰写一篇学术论文初稿，包含摘要、关键词、研究背景、文献综述、研究方法、分析讨论、结论与参考文献，并规范标注引用来源。", ResultSummary: "将研究资料组织成有引用、有章节结构的论文初稿，便于后续审阅。", ResultHighlights: []string{"摘要、关键词和研究背景", "文献综述、方法与分析讨论", "结论、局限与参考文献"},
		Steps: steps("确定论文问题", "明确研究主题、对象和论证范围")},
	{
		ID: "novel", Title: "悬疑短篇小说创作", Description: "根据人物与情节设定，创作节奏紧凑的完整悬疑故事", Category: "文档写作", OutputType: "document", OutputLabel: "短篇小说", ImageURL: "/showcase/14-mystery-novel.png", AttachmentHint: "人物与情节设定.docx",
		PromptShort: "根据人物与情节设定，创作一篇线索严密、结局反转的悬疑短篇小说。", Prompt: "请根据我上传的人物与情节设定创作一篇悬疑短篇小说，通过环境细节、人物行动和关键物证逐步铺设线索，保持紧凑的叙事节奏，并在结尾给出合理且令人意外的反转。", ResultSummary: "从人物动机、线索网络和叙事节奏出发，完成可继续修改的小说初稿。", ResultHighlights: []string{"人物关系、动机和秘密", "线索、误导和揭示顺序", "合理且意外的结尾反转"},
		Steps: steps("读取人物设定", "提取人物关系、动机、秘密与冲突")},
}

type showcaseCaseTranslation struct {
	Title                string
	Description          string
	Category             string
	PrimaryCategory      string
	SecondaryOptions     []ShowcaseCaseOption
	OutputLabel          string
	AttachmentHint       string
	PromptShort          string
	Prompt               string
	ResultSummary        string
	ResultHighlights     []string
	FirstStepTitle       string
	FirstStepDescription string
	Steps                []ShowcaseCaseStep
	Tasks                []ShowcaseCaseTask
}

var showcaseCaseTranslationsEnUS = map[string]showcaseCaseTranslation{
	"aiProduct": {
		Title: "Product design and PRD generation", Description: "Turn user needs into an executable Agent product plan", Category: "Document writing", PrimaryCategory: "Product design and PRD generation", SecondaryOptions: []ShowcaseCaseOption{
			{ID: "full", Label: "Full feature", Description: "Cover the complete task flow and delivery standards", Prompt: "Cover the complete task flow and delivery standards, and produce a review-ready plan."},
			{ID: "outline", Label: "Quick draft", Description: "Start with a structured first draft", Prompt: "Start with a structured first draft by confirming the core conclusions and execution framework before adding detail."},
		}, OutputLabel: "Product design plan", AttachmentHint: "AI Agent product requirements.docx",
		PromptShort: "Design an AI Agent product that can execute complex tasks continuously from the product requirements.", Prompt: "Design an AI Agent product for knowledge workers based on the product requirements I uploaded. Cover target users, core scenarios, value proposition, task flow, information architecture, key interactions, exception handling, trust mechanisms, and core metrics, then output a complete plan for product review.", ResultSummary: "Turn user goals into an executable, trackable, and deliverable Agent product plan.", ResultHighlights: []string{"Target users and core scenarios", "Task flow and information architecture", "Key interactions, exceptions, and metrics"}, FirstStepTitle: "Understand the product requirements", FirstStepDescription: "Identify target users, scenarios, and constraints",
		Steps: []ShowcaseCaseStep{
			{Title: "Analyze product requirements", Description: "Identify target users, core problems, and business constraints"},
			{Title: "Map task scenarios", Description: "Extract frequent tasks, user journeys, and key touchpoints"},
			{Title: "Design product structure", Description: "Plan information architecture, task flow, and core interactions"},
			{Title: "Add trust mechanisms", Description: "Design plan confirmation, process control, and exception handling"},
			{Title: "Output the design plan", Description: "Organize the product brief, metrics, and review focus"},
		}, Tasks: []ShowcaseCaseTask{
			{ID: "product-design", Title: "Product design", Description: "Design product structure, task flow, and key interactions from users and scenarios.", OutputLabel: "Product design plan", Prompt: "Complete the AI Agent product design from the product requirements I uploaded, focusing on target users, core scenarios, task flow, information architecture, and key interactions."},
			{ID: "competitor-analysis", Title: "Competitor analysis", Description: "Study representative products, capabilities, and strategies to identify differentiation opportunities.", OutputLabel: "Competitor research report", Prompt: "Complete a competitor analysis from the product requirements and competitor materials I uploaded. Compare positioning, capabilities, target users, and differentiation opportunities."},
			{ID: "product-comparison", Title: "Product comparison report", Description: "Compare multiple products on consistent dimensions and provide clear selection guidance.", OutputLabel: "Product comparison report", Prompt: "Generate a product comparison report from the competitor materials I uploaded. Compare capabilities, scenarios, cost, and differentiation on consistent dimensions, then provide selection guidance."},
			{ID: "prd-generation", Title: "PRD generation", Description: "Turn the product concept into a reviewable, buildable PRD with acceptance criteria.", OutputLabel: "Product requirements document", Prompt: "Generate a complete PRD from the product requirements and analysis I uploaded, including functional requirements, interactions, exception handling, acceptance criteria, metrics, and version planning."},
		},
	},
	"knowledgeQa": {
		Title: "Knowledge-base Q&A solution", Description: "Plan knowledge ingestion, retrieval, Q&A, and permission governance", Category: "Document writing", PrimaryCategory: "Knowledge-base Q&A", OutputLabel: "Solution plan", AttachmentHint: "Knowledge-base materials and requirements.zip",
		PromptShort: "Design a traceable enterprise knowledge-base Q&A solution from business materials and requirements.", Prompt: "Design an enterprise knowledge-base Q&A solution from the knowledge materials and business requirements I uploaded. Cover ingestion and cleaning, document parsing, chunking, hybrid retrieval, answer generation, source citations, permission isolation, evaluation, operations, and a phased rollout plan.", ResultSummary: "Create a practical and traceable enterprise knowledge solution from ingestion to cited answers.", ResultHighlights: []string{"Data ingestion and document parsing", "Hybrid retrieval, citations, and permissions", "Evaluation and operations loop"}, FirstStepTitle: "Inventory knowledge sources", FirstStepDescription: "Identify document types, permissions, and update frequency",
	},
	"industry": {
		Title: "Market research and competitor analysis", Description: "Analyze market size, consumer trends, and brand landscape in a complete report", Category: "Research", OutputLabel: "Web report", AttachmentHint: "Pet supplies market materials.pdf",
		PromptShort: "Research the domestic pet supplies market, including market size, consumer trends, key brands, and future opportunities.", Prompt: "Research the domestic pet supplies market using the materials I provided. Analyze market size, consumer segments, category trends, major brands, competitive landscape, and future opportunities, then generate a structured market research report with sources.", ResultSummary: "Produce source-backed research judgments around market size, user trends, and competition.", ResultHighlights: []string{"Market size and consumer segments", "Category trends and competition", "Market opportunities and entry recommendations"}, FirstStepTitle: "Define research questions", FirstStepDescription: "Clarify market scope, users, and evaluation criteria",
	},
	"sales": {
		Title: "Sales data extraction and analysis", Description: "Find the causes of regional sales decline and create a dashboard with recommendations", Category: "Data analysis", OutputLabel: "Data dashboard", AttachmentHint: "East China sales data.xlsx",
		PromptShort: "Analyze East China sales data to identify the key causes of declining sales and propose improvements.", Prompt: "Analyze the East China sales data I uploaded. Compare sales, order volume, average order value, and year-over-year changes by city, product, and channel. Identify the key causes of the decline and generate a diagnostic report with charts, conclusions, and improvement recommendations.", ResultSummary: "Turn scattered sales data into explainable metric changes, root causes, and actions.", ResultHighlights: []string{"City, product, and channel breakdown", "Sales, orders, and average order value", "Root-cause analysis and improvements"}, FirstStepTitle: "Read the data structure", FirstStepDescription: "Identify fields, time range, and missing values",
	},
	"ppt": {
		Title: "Personal annual review deck", Description: "Turn a year's experiences, achievements, and growth into a complete personal report", Category: "PPT creation", OutputLabel: "PPT preview", AttachmentHint: "Personal annual notes.docx",
		PromptShort: "Create a personal annual review deck from my notes, showing experiences, achievements, growth, and plans.", Prompt: "Create a personal annual review deck from the annual notes I uploaded. Build a narrative around annual themes, important experiences, key achievements, capability growth, memorable moments, reflection, and next-year plans, using a concise, warm, and personal visual style.", ResultSummary: "Organize annual notes into a narrative, presentable, and editable report.", ResultHighlights: []string{"Annual themes and narrative", "Achievements, growth, and reflection", "Plans for the next year"}, FirstStepTitle: "Collect annual notes", FirstStepDescription: "Extract experiences, achievements, and growth signals",
	},
	"proposal": {
		Title: "Lifestyle review copy for Xiaohongshu", Description: "Extract store highlights and experiences into natural, engaging promotional copy", Category: "Content creation", OutputLabel: "Store review copy", AttachmentHint: "Store photos and information.zip",
		PromptShort: "Write a natural and appealing Xiaohongshu store review from the photos and store information.", Prompt: "Write a natural Xiaohongshu store review from the store photos and information I uploaded. Extract atmosphere, signature products, real experiences, price, and transport details, and add titles and relevant hashtags.", ResultSummary: "Keep real experiences and practical information in a publishable first draft.", ResultHighlights: []string{"Multiple titles and openings", "Atmosphere, signature products, and experience", "Hashtags and image suggestions"}, FirstStepTitle: "Identify store information", FirstStepDescription: "Extract atmosphere, products, price, and transport",
	},
	"stickers": {
		Title: "Creative images and sticker generation", Description: "Cover image generation, local editing, and sticker creation for fast visual work", Category: "Image design", OutputLabel: "Image candidates", AttachmentHint: "Visual requirements and reference images.zip",
		PromptShort: "Generate a set of high-quality, consistent images from visual requirements and reference images.", Prompt: "Generate a set of high-quality images from the visual requirements and reference images I uploaded. Accurately express the subject, scene, composition, lighting, color, and style, and provide candidates for different use cases.", ResultSummary: "Produce comparable visual candidates with consistent subjects, style, and camera language.", ResultHighlights: []string{"Subject, scene, and composition", "Style, lighting, and color options", "Candidate images for multiple scenarios"}, FirstStepTitle: "Parse visual requirements", FirstStepDescription: "Extract subject, style, composition, and purpose",
	},
	"landing": {
		Title: "Smartwatch product handbook", Description: "Organize product specifications, features, and scenarios into an interactive digital handbook", Category: "Web creation", OutputLabel: "Web handbook", AttachmentHint: "Smartwatch product materials.docx",
		PromptShort: "Create a consumer-facing digital product handbook for a smartwatch from the product materials.", Prompt: "Design and create an interactive digital product handbook for consumers from the smartwatch materials I uploaded. Present design, health monitoring, sports modes, battery life, compatibility, specifications, and typical scenarios, with both desktop and mobile layouts.", ResultSummary: "Organize product specifications and scenarios into a consumer-friendly digital handbook.", ResultHighlights: []string{"Hero benefits and product story", "Features, specifications, and scenarios", "Desktop and mobile layouts"}, FirstStepTitle: "Organize product materials", FirstStepDescription: "Extract benefits, specifications, and typical scenarios",
	},
	"meeting": {
		Title: "Daily scheduled news brief", Description: "Summarize AI industry updates on a schedule into a concise daily brief", Category: "Productivity", OutputLabel: "Scheduled news brief",
		PromptShort: "Create a daily 9 AM task that summarizes and delivers AI industry news.", Prompt: "Create a scheduled task that runs at 9 AM every day. Summarize important AI product launches, model updates, industry funding, and policy developments from the previous 24 hours, select the most relevant items, and produce a concise brief with source links.", ResultSummary: "Turn scope, schedule, selection rules, and delivery target into a task draft for confirmation.", ResultHighlights: []string{"Runs daily at 09:00", "Products, models, funding, and policy", "Deduplication, verification, and source links"}, FirstStepTitle: "Define the news scope", FirstStepDescription: "Confirm topics of interest and time window",
	},
	"competitor": {
		Title: "Domestic AI office assistant comparison", Description: "Compare mainstream products by features, positioning, price, and differentiation", Category: "Research", OutputLabel: "Comparison report", AttachmentHint: "AI office assistant list.xlsx",
		PromptShort: "Research mainstream domestic AI office assistants and compare their features, pricing, and target users.", Prompt: "Research mainstream domestic AI office assistants. Compare positioning, target users, core features, document and data capabilities, pricing, business model, and marketing, then output a sourced comparison table and summarize each product's differentiation.", ResultSummary: "Compare product capabilities and business models on consistent dimensions for clear selection guidance.", ResultHighlights: []string{"Positioning and target users", "Features, documents, and data capabilities", "Pricing, business model, and differentiation"}, FirstStepTitle: "Set the competitor scope", FirstStepDescription: "Select direct competitors and alternatives",
	},
	"policy": {
		Title: "Track policy changes and extract impact", Description: "Summarize authoritative policy information and highlight business-relevant changes", Category: "Research", OutputLabel: "Policy brief", AttachmentHint: "Sample policy list.pdf",
		PromptShort: "Track recent policy changes in a target area and assess their business, compliance, and opportunity impact.", Prompt: "Research important recent policy changes in [policy topic], prioritizing government and authoritative sources. Explain the background, major changes, affected parties, effective dates, and opportunities, risks, and actions for [target business].", ResultSummary: "Turn policy text and effective dates into actionable business risks and opportunities.", ResultHighlights: []string{"Authoritative sources and policy background", "Changes, affected parties, and effective dates", "Business opportunities, risks, and actions"}, FirstStepTitle: "Confirm the tracking topic", FirstStepDescription: "Define policy scope, business, and affected parties",
	},
	"feedback": {
		Title: "Summarize user feedback and product opportunities", Description: "Cluster problems across large volumes of feedback and identify high-value opportunities", Category: "Data analysis", OutputLabel: "Insight dashboard", AttachmentHint: "Sample user feedback.csv",
		PromptShort: "Analyze user feedback to find frequent issues, sentiment changes, and the highest-priority product opportunities.", Prompt: "Analyze the [user feedback data] I uploaded. Cluster themes, analyze sentiment, and count issue frequency. Identify high-impact and frequent issues, then prioritize product opportunities by user value, business value, and implementation cost.", ResultSummary: "Create a sortable product opportunity list from themes, sentiment, and frequency.", ResultHighlights: []string{"Theme clusters and issue frequency", "Sentiment changes and high-impact issues", "Opportunities ranked by value and cost"}, FirstStepTitle: "Clean the feedback data", FirstStepDescription: "Deduplicate and identify valid feedback",
	},
	"article": {
		Title: "Turn interview material into a feature article", Description: "Extract viewpoints and storylines into a publishable first draft", Category: "Content creation", OutputLabel: "Article draft", AttachmentHint: "Sample interview notes.docx",
		PromptShort: "Turn interview notes into a clear, readable feature article with the core viewpoints intact.", Prompt: "Write a feature article for [target readers] from the [interview notes] I uploaded. Preserve important viewpoints and representative expressions, build a clear storyline, and add a lead, subheadings, and closing summary.", ResultSummary: "Preserve key interview expressions while building a reader-focused article structure.", ResultHighlights: []string{"Core viewpoints and representative expressions", "Lead, storyline, and subheadings", "Editable article draft"}, FirstStepTitle: "Organize interview material", FirstStepDescription: "Identify people, viewpoints, facts, and expressions",
	},
	"weekly": {
		Title: "Generate a weekly report from work notes", Description: "Organize scattered notes into achievements, issues, and next-week plans", Category: "Productivity", OutputLabel: "Structured weekly report", AttachmentHint: "Sample work notes.txt",
		PromptShort: "Turn scattered work notes into a focused weekly report suitable for upward reporting.", Prompt: "Turn the [weekly work notes] I provided into a focused weekly report for upward reporting. Cover achievements, key progress, issues and risks, requests for help, and next-week plans, highlighting measurable results.", ResultSummary: "Extract achievements, risks, and action plans from scattered notes into a report.", ResultHighlights: []string{"Achievements and key progress", "Issues, risks, and collaboration needs", "Next-week plans and measurable results"}, FirstStepTitle: "Collect work notes", FirstStepDescription: "Group tasks, meetings, and progress by project",
	},
	"paper": {
		Title: "Academic paper writing", Description: "Combine research materials and references into a structured academic paper draft", Category: "Document writing", OutputLabel: "Paper draft", AttachmentHint: "Research materials and references.zip",
		PromptShort: "Write a first draft of an academic paper on generative AI in education from research materials and references.", Prompt: "Write an academic paper draft from the research materials and references I uploaded about how generative AI supports personalized learning. Include an abstract, keywords, background, literature review, methods, analysis, discussion, conclusion, and references with properly marked citations.", ResultSummary: "Organize research materials into a cited, structured paper draft for review.", ResultHighlights: []string{"Abstract, keywords, and background", "Literature review, methods, and analysis", "Conclusion, limitations, and references"}, FirstStepTitle: "Define the research question", FirstStepDescription: "Clarify topic, subjects, and argument scope",
	},
	"novel": {
		Title: "Mystery short story creation", Description: "Create a complete, tightly paced mystery from character and plot settings", Category: "Document writing", OutputLabel: "Short story", AttachmentHint: "Character and plot settings.docx",
		PromptShort: "Write a tightly plotted mystery short story with a surprising but logical ending from the character and plot settings.", Prompt: "Write a mystery short story from the character and plot settings I uploaded. Plant clues through environmental details, character actions, and key evidence, keep the narrative pace tight, and end with a logical yet surprising twist.", ResultSummary: "Complete an editable story draft from character motives, clues, and narrative pacing.", ResultHighlights: []string{"Relationships, motives, and secrets", "Clues, misdirection, and reveals", "A logical and surprising twist"}, FirstStepTitle: "Read the character settings", FirstStepDescription: "Extract relationships, motives, secrets, and conflicts",
	},
}

var showcaseCategoriesEnUS = []string{
	"All",
	"Research",
	"Data analysis",
	"PPT creation",
	"Document writing",
	"Content creation",
	"Image design",
	"Web creation",
	"Productivity",
}

func localizeCase(item ShowcaseCase, locale string) ShowcaseCase {
	if locale != common.LocaleEnUS {
		return item
	}
	translation, ok := showcaseCaseTranslationsEnUS[item.ID]
	if !ok {
		return item
	}
	item.Title = translation.Title
	item.Description = translation.Description
	item.Category = translation.Category
	if translation.PrimaryCategory != "" {
		item.PrimaryCategory = translation.PrimaryCategory
	}
	if translation.SecondaryOptions != nil {
		item.SecondaryOptions = translation.SecondaryOptions
	}
	item.OutputLabel = translation.OutputLabel
	item.AttachmentHint = translation.AttachmentHint
	item.PromptShort = translation.PromptShort
	item.Prompt = translation.Prompt
	item.ResultSummary = translation.ResultSummary
	item.ResultHighlights = translation.ResultHighlights
	if translation.Steps != nil {
		item.Steps = translation.Steps
	} else {
		item.Steps = localizedSteps(translation.FirstStepTitle, translation.FirstStepDescription)
	}
	if translation.Tasks != nil {
		item.Tasks = translation.Tasks
	}
	return item
}

func localizedSteps(firstTitle, firstDescription string) []ShowcaseCaseStep {
	return []ShowcaseCaseStep{
		{Title: firstTitle, Description: firstDescription},
		{Title: "Break down the execution plan", Description: "Confirm scope, input materials, and delivery standards"},
		{Title: "Execute and organize results", Description: "Use materials and tools to produce intermediate conclusions"},
		{Title: "Output an editable deliverable", Description: "Keep key conclusions, sources, and follow-up entry points"},
	}
}

func steps(title, description string) []ShowcaseCaseStep {
	return []ShowcaseCaseStep{
		{Title: title, Description: description},
		{Title: "拆解执行计划", Description: "确认任务范围、输入资料和交付标准"},
		{Title: "执行并整理结果", Description: "结合资料和工具逐步生成中间结论"},
		{Title: "输出可继续编辑的产物", Description: "保留关键结论、来源和后续追问入口"},
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func ListCases(w http.ResponseWriter, r *http.Request) {
	locale := common.NormalizeLocale(r.Header.Get("Accept-Language"))
	common.SetLanguageResponseHeaders(w, locale)
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("keyword")))
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	filtered := make([]ShowcaseCase, 0, len(showcaseCases))
	for _, item := range showcaseCases {
		localized := localizeCase(item, locale)
		if category != "" && category != "全部" && category != "All" && item.Category != category && localized.Category != category {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(strings.Join([]string{item.Title, item.Description, item.Category, item.PromptShort, localized.Title, localized.Description, localized.Category, localized.PromptShort}, " ")), query) {
			continue
		}
		filtered = append(filtered, localized)
	}
	responseCategories := categories
	if locale == common.LocaleEnUS {
		responseCategories = showcaseCategoriesEnUS
	}
	writeJSON(w, http.StatusOK, ShowcaseCaseListResponse{Cases: filtered, Categories: responseCategories, Total: len(filtered)})
}

func GetCase(w http.ResponseWriter, r *http.Request) {
	locale := common.NormalizeLocale(r.Header.Get("Accept-Language"))
	common.SetLanguageResponseHeaders(w, locale)
	id := strings.TrimSpace(common.PathVar(r, "case_id"))
	for _, item := range showcaseCases {
		if item.ID == id {
			writeJSON(w, http.StatusOK, localizeCase(item, locale))
			return
		}
	}
	common.ReplyErr(w, "not found", http.StatusNotFound)
}
