import type { StructuredAsset } from '../../shared';
import { getMarketCategory, type MarketCategory } from './skillMarketCategories';
import type { SkillIconName } from './SkillIcon';

// Specific purpose takes precedence over the category icon; preserve stable builtin IDs.
export const builtinMarketSkillIcons: Record<string, SkillIconName> = {
  'bsk_01KPERSONALDOCSEARCH20260923': 'files', // personal-document-search
  'bsk_94D43FFF92D2D888AAB1C848CD95': 'orbit-search', // deep-research
  'bsk_D3B3BB403C7E227D6C9A85F9379F': 'file-check', // single-document-review
  'bsk_D299B47043E38D56EDCF90DAB345': 'book-check', // systematic-document-and-literature-review
  'bsk_F58F9663671AA7A813013A95031E': 'file-search', // paper-search
  'bsk_892318A1DDDD61AEB4ED646F38B8': 'telescope', // sciverse-paper-search
  'bsk_D0F182830293F16061539CC953C9': 'globe-search', // web-search
  'bsk_0ABF3D273214B87F28FBCA4C12B4': 'terminal', // browsh
  'bsk_00B8B87803B41288D87519C5BDCA': 'cloud-download', // Scrape
  'bsk_88567A6C00D688B2B3D6D4592768': 'compass', // baidu-search
  'bsk_61DEE792D6D8288DCAC0D522E0BB': 'decision-board', // design-harness
  'bsk_EF6E2CC153DFF8C85944DA8F0302': 'feather', // humanizer
  'bsk_BE0FF3A37156383A87D08C50E49C': 'notebook-pen', // structured-longform-writing
  'bsk_9C56277F7EF088E1D66A9CD25C78': 'languages', // Translate
  'bsk_3E6DEC2AEAE730792A04F96D23D6': 'stamp', // govwriter-pro
  'bsk_A43C387D49621BAE0DBC7D0C34FD': 'file-print', // PDF Generator
  'bsk_0E9EBC266116D6F0139A6386DA98': 'file-edit', // academic-natural-rewrite
  'bsk_3E5FE38A64F5A2170206E3B4644A': 'notebook-pen', // academic-paper-assistant
  'bsk_399CB55E1063FF5F6581D9A92760': 'notebook-text', // blog-writer
  'bsk_43BB5FAD4A760FA7DD74C1656EDA': 'book-star', // novel-evaluator
  'bsk_E39B3BA0F9C30314CFBE3EA5B962': 'type-lines', // gongwenformat-pro
  'bsk_5D526F2BCBD3F42333E839480008': 'code-file', // superpowers
  'bsk_A441998969872C04258E405F9DED': 'terminal', // openclaw-cli
  'bsk_3363621D9FA34F6C1A8CCC464E29': 'key-terminal', // ssh-essentials
  'bsk_58FB17365FABDFC022C799F99641': 'chip', // system-info
  'bsk_802B99BE836991F6BE06EC20FA65': 'api', // api-tester
  'bsk_0085494C730FD1D0A4B448D4327D': 'code-check', // critical-code-reviewer
  'bsk_A9AE72B3519B9F56AAEF12207195': 'git-branch', // DevOps
  'bsk_2C98A67266ED731CD6EC11C893F6': 'review-chat', // receiving-code-review
  'bsk_EEC91A1759AD80B74467126589FC': 'shield-check', // safe-exec
  'bsk_28DCE83C76F0C1A068FC231620B2': 'code-file', // Code
  'bsk_308C67E7F0BCA7A481C66C667E92': 'test-tube', // swift-testing-expert
  'bsk_DD1488E1CFC5157565FB6E06EDB2': 'chip', // konnect
  'bsk_E17A27B519E2045694D73A77A77D': 'scissors', // simplify-codebase
  'bsk_9694076690497446BFC88D7E8B60': 'layers', // compose-skill
  'bsk_A916D8A640F9C63192C296EC48AE': 'shield-check', // torvalds-doctrine
  'bsk_BDFEC6FD3EA622F64D82EEC6C9DC': 'bolt-code', // ultracode
  'bsk_37FFD4F3177E98004E65A93931B8': 'video', // beatra-ai-video-studio
  'bsk_D86755FB5C9BF0CD07F3CDBC51FA': 'cover-image', // wechat-channels-cover-maker
  'bsk_879B61F2B775A99BEA3B0190218D': 'scissors', // video-clip-assistant
  'bsk_BA88DF5EF2C7B8A3EDC86BB83681': 'animation', // web-animation-design
  'bsk_CFAB0960D73A2BF7DA9D8B57BB51': 'layout', // frontend-design
  'bsk_77746FE46D364CCD57FBED23FE58': 'code-window', // frontend-design
  'bsk_8D1D50E2499072E9D3A1517F1687': 'swatches', // extract-design-system
  'bsk_C789DC9F85A2341BCEED0320E98D': 'file-link', // feishu
  'bsk_7FF63C7EC55E35FF3C46E7E69702': 'spreadsheet', // xlsx-cn
  'bsk_E0D17ED0B1839E52A7891792856B': 'folder-sort', // file-organizer-zh
  'bsk_2AAD11E35B46EECC3B4B8D5A8137': 'files', // duplicate-file-cleaner
  'bsk_6CD2DF685B7BDFAB1326739BF2E4': 'chart-line', // data-analyst-cn
  'bsk_6DC33CC9892589B50C1AE272DA0A': 'decision-board', // high-stakes-analytics-decision-lab
  'bsk_4CCCDEB336A7DF829210858CAA36': 'scan-search', // find-skill-skillhub
  'bsk_7D37DF667E01E35AECA53D0C5FC0': 'brain-cycle', // Self-Evolving Skill
  'bsk_0E39244BBC1CF2DD2476C8A35E06': 'chart-line', // Capability Evolver
  'bsk_568D482AB3F369FDFC313471FA7F': 'agent-nodes', // agent-orchestrator
  'bsk_96E1CC6DE159A6D41BB645CD0B6B': 'brain-cycle', // self-evolution
  'bsk_ADA4E3C5A29E32ADF544E8D750E3': 'browser-pointer', // agent-browser
  'bsk_40FC9A5644E19DB1BD40D5E4F0D6': 'brain', // adaptive-reasoning
  'bsk_FAED30A7BF12C6509578E0729030': 'task-tree', // task-decomposer
  'bsk_C9F5AF4B5B3845FC6F1EB196EAB5': 'wrench', // luban-skill
  'bsk_19F8611F57F50625984D00635BAF': 'brain', // self-reflection
  'bsk_768062BF68808276B0934EBD3C90': 'book-check', // using-superpowers
  'bsk_1DE29AC068B700D3F590354CBA3E': 'prompt', // prompt-architect
  'bsk_A769BA5BDCFC981DC4E6DB4F44F9': 'notebook-pen', // skill-conductor
  'bsk_CFFD05135BE9E2BE71E627250692': 'file-edit', // seo-content-writer
  'bsk_085577A1BB1D953D5F016235699C': 'calendar', // Social Media Scheduler
  'bsk_E0710DE69DB2A62E4031C9C6E8FA': 'radar', // brand-abi-check
  'bsk_485E8BD0C9D66D9B84EE4250B519': 'video', // bilibili-monitor
  'bsk_73C33713004A156414072D5CDD69': 'coins', // crypto-self-learning
  'bsk_3365D16340830BB483DE0A322275': 'target', // product-marketing-context
  'bsk_310B310FFCB5CD1F7008DD49403A': 'social', // social-media-agent
  'bsk_004EB2D1ACE186C2C9E7F8DEB621': 'building', // one-person-company
  'bsk_1830BE46A3C1C3DD91B70242C32A': 'calculator', // financial-calculator
  'bsk_E5BC6DABA0FAAD8289162068B3FB': 'compare', // competitive-intelligence-market-research
  'bsk_8AD3C1DE06485264481774333079': 'profile-chart', // creator-account-teardown
  'bsk_318265EE8676FED41006128ADB05': 'target', // baobiao-recommend-bid-opportunities
  'bsk_98475A533DCDB628F8EC518D3A59': 'file-search', // bid-query-analysis-workbench
  'bsk_3BB7A3ACE8466751F7475080EF56': 'type-lines', // copywriting
  'bsk_5031C5913936B1696201131A3BED': 'flame', // hot-topic-content-maker
  'bsk_A04C5A6E4A2B33421061CAD4D287': 'chart-line', // Market Research
  'bsk_DA85C5CDD3D38BCC926436321714': 'chat-search', // wechat-search
  'bsk_CB52793C6701B3920A4A73357411': 'building', // 拟在建项目与招投标前置商机挖掘
  'bsk_2FF60AD350330792C465BB3C6499': 'megaphone', // marketing-os
  'bsk_91B2EEAF6D4A17B1C830FAE4806D': 'mail', // email-marketing-bible
  'bsk_73A60A8EA7C5164946158B234C3C': 'cards', // vocabulary-learning
  'bsk_90F7A661AB776A5D0687D6BF8A30': 'lightbulb', // thinking-partner
  'bsk_81AA7A4A50A371FE26519666ED65': 'languages', // language-learning
  'bsk_274C622882A6DDE479EA8174C31C': 'book-star', // Learning
  'bsk_0E2B72DA5B5F8BB2CF80A92ED80C': 'graduation', // yupi-skill
  'bsk_3A4CCB090C1C3CCFDEED53961124': 'dialogue', // socrates
};

// New/custom skills without a curated icon use a different shape in the same family.
const categorySkillIcons: Record<MarketCategory, SkillIconName> = {
  research: 'file-search', writing: 'notebook-pen', development: 'code-file', design: 'cover-image',
  office: 'spreadsheet', automation: 'agent-nodes', business: 'target', learning: 'graduation',
};

export function getMarketSkillIcon(item: StructuredAsset): SkillIconName {
  return Object.prototype.hasOwnProperty.call(builtinMarketSkillIcons, item.id)
    ? builtinMarketSkillIcons[item.id] : categorySkillIcons[getMarketCategory(item)];
}
