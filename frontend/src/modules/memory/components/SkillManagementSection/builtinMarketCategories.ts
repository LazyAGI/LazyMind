import type { MarketCategory } from './skillMarketCategories';

// Reviewed against each visible builtin’s description, keyed by UID so same-name skills remain distinct.
export const builtinMarketCategories: Record<string, MarketCategory> = {
  'bsk_01KPERSONALDOCSEARCH20260923': 'research', // personal-document-search
  'bsk_94D43FFF92D2D888AAB1C848CD95': 'research', // deep-research
  'bsk_D3B3BB403C7E227D6C9A85F9379F': 'research', // single-document-review
  'bsk_D299B47043E38D56EDCF90DAB345': 'research', // systematic-document-and-literature-review
  'bsk_F58F9663671AA7A813013A95031E': 'research', // paper-search
  'bsk_892318A1DDDD61AEB4ED646F38B8': 'research', // sciverse-paper-search
  'bsk_D0F182830293F16061539CC953C9': 'research', // web-search
  'bsk_0ABF3D273214B87F28FBCA4C12B4': 'research', // browsh
  'bsk_00B8B87803B41288D87519C5BDCA': 'research', // Scrape
  'bsk_88567A6C00D688B2B3D6D4592768': 'research', // baidu-search
  'bsk_61DEE792D6D8288DCAC0D522E0BB': 'research', // design-harness
  'bsk_EF6E2CC153DFF8C85944DA8F0302': 'writing', // humanizer
  'bsk_BE0FF3A37156383A87D08C50E49C': 'writing', // structured-longform-writing
  'bsk_9C56277F7EF088E1D66A9CD25C78': 'writing', // Translate
  'bsk_3E6DEC2AEAE730792A04F96D23D6': 'writing', // govwriter-pro
  'bsk_A43C387D49621BAE0DBC7D0C34FD': 'writing', // PDF Generator
  'bsk_0E9EBC266116D6F0139A6386DA98': 'writing', // academic-natural-rewrite
  'bsk_3E5FE38A64F5A2170206E3B4644A': 'writing', // academic-paper-assistant
  'bsk_399CB55E1063FF5F6581D9A92760': 'writing', // blog-writer
  'bsk_43BB5FAD4A760FA7DD74C1656EDA': 'writing', // novel-evaluator
  'bsk_E39B3BA0F9C30314CFBE3EA5B962': 'writing', // gongwenformat-pro
  'bsk_5D526F2BCBD3F42333E839480008': 'development', // superpowers
  'bsk_A441998969872C04258E405F9DED': 'development', // openclaw-cli
  'bsk_3363621D9FA34F6C1A8CCC464E29': 'development', // ssh-essentials
  'bsk_58FB17365FABDFC022C799F99641': 'development', // system-info
  'bsk_802B99BE836991F6BE06EC20FA65': 'development', // api-tester
  'bsk_0085494C730FD1D0A4B448D4327D': 'development', // critical-code-reviewer
  'bsk_A9AE72B3519B9F56AAEF12207195': 'development', // DevOps
  'bsk_2C98A67266ED731CD6EC11C893F6': 'development', // receiving-code-review
  'bsk_EEC91A1759AD80B74467126589FC': 'development', // safe-exec
  'bsk_28DCE83C76F0C1A068FC231620B2': 'development', // Code
  'bsk_308C67E7F0BCA7A481C66C667E92': 'development', // swift-testing-expert
  'bsk_DD1488E1CFC5157565FB6E06EDB2': 'development', // konnect
  'bsk_E17A27B519E2045694D73A77A77D': 'development', // simplify-codebase
  'bsk_9694076690497446BFC88D7E8B60': 'development', // compose-skill
  'bsk_A916D8A640F9C63192C296EC48AE': 'development', // torvalds-doctrine
  'bsk_BDFEC6FD3EA622F64D82EEC6C9DC': 'development', // ultracode
  'bsk_37FFD4F3177E98004E65A93931B8': 'design', // beatra-ai-video-studio
  'bsk_D86755FB5C9BF0CD07F3CDBC51FA': 'design', // wechat-channels-cover-maker
  'bsk_879B61F2B775A99BEA3B0190218D': 'design', // video-clip-assistant
  'bsk_BA88DF5EF2C7B8A3EDC86BB83681': 'design', // web-animation-design
  'bsk_CFAB0960D73A2BF7DA9D8B57BB51': 'design', // frontend-design
  'bsk_77746FE46D364CCD57FBED23FE58': 'design', // frontend-design
  'bsk_8D1D50E2499072E9D3A1517F1687': 'design', // extract-design-system
  'bsk_C789DC9F85A2341BCEED0320E98D': 'office', // feishu
  'bsk_7FF63C7EC55E35FF3C46E7E69702': 'office', // xlsx-cn
  'bsk_E0D17ED0B1839E52A7891792856B': 'office', // file-organizer-zh
  'bsk_2AAD11E35B46EECC3B4B8D5A8137': 'office', // duplicate-file-cleaner
  'bsk_6CD2DF685B7BDFAB1326739BF2E4': 'office', // data-analyst-cn
  'bsk_6DC33CC9892589B50C1AE272DA0A': 'office', // high-stakes-analytics-decision-lab
  'bsk_4CCCDEB336A7DF829210858CAA36': 'automation', // find-skill-skillhub
  'bsk_7D37DF667E01E35AECA53D0C5FC0': 'automation', // Self-Evolving Skill
  'bsk_0E39244BBC1CF2DD2476C8A35E06': 'automation', // Capability Evolver
  'bsk_568D482AB3F369FDFC313471FA7F': 'automation', // agent-orchestrator
  'bsk_96E1CC6DE159A6D41BB645CD0B6B': 'automation', // self-evolution
  'bsk_ADA4E3C5A29E32ADF544E8D750E3': 'automation', // agent-browser
  'bsk_40FC9A5644E19DB1BD40D5E4F0D6': 'automation', // adaptive-reasoning
  'bsk_FAED30A7BF12C6509578E0729030': 'automation', // task-decomposer
  'bsk_C9F5AF4B5B3845FC6F1EB196EAB5': 'automation', // luban-skill
  'bsk_19F8611F57F50625984D00635BAF': 'automation', // self-reflection
  'bsk_768062BF68808276B0934EBD3C90': 'automation', // using-superpowers
  'bsk_1DE29AC068B700D3F590354CBA3E': 'automation', // prompt-architect
  'bsk_A769BA5BDCFC981DC4E6DB4F44F9': 'automation', // skill-conductor
  'bsk_CFFD05135BE9E2BE71E627250692': 'business', // seo-content-writer
  'bsk_085577A1BB1D953D5F016235699C': 'business', // Social Media Scheduler
  'bsk_E0710DE69DB2A62E4031C9C6E8FA': 'business', // brand-abi-check
  'bsk_485E8BD0C9D66D9B84EE4250B519': 'business', // bilibili-monitor
  'bsk_73C33713004A156414072D5CDD69': 'business', // crypto-self-learning
  'bsk_3365D16340830BB483DE0A322275': 'business', // product-marketing-context
  'bsk_310B310FFCB5CD1F7008DD49403A': 'business', // social-media-agent
  'bsk_004EB2D1ACE186C2C9E7F8DEB621': 'business', // one-person-company
  'bsk_1830BE46A3C1C3DD91B70242C32A': 'business', // financial-calculator
  'bsk_E5BC6DABA0FAAD8289162068B3FB': 'business', // competitive-intelligence-market-research
  'bsk_8AD3C1DE06485264481774333079': 'business', // creator-account-teardown
  'bsk_318265EE8676FED41006128ADB05': 'business', // baobiao-recommend-bid-opportunities
  'bsk_98475A533DCDB628F8EC518D3A59': 'business', // bid-query-analysis-workbench
  'bsk_3BB7A3ACE8466751F7475080EF56': 'business', // copywriting
  'bsk_5031C5913936B1696201131A3BED': 'business', // hot-topic-content-maker
  'bsk_A04C5A6E4A2B33421061CAD4D287': 'business', // Market Research
  'bsk_DA85C5CDD3D38BCC926436321714': 'business', // wechat-search
  'bsk_CB52793C6701B3920A4A73357411': 'business', // 拟在建项目与招投标前置商机挖掘
  'bsk_2FF60AD350330792C465BB3C6499': 'business', // marketing-os
  'bsk_91B2EEAF6D4A17B1C830FAE4806D': 'business', // email-marketing-bible
  'bsk_73A60A8EA7C5164946158B234C3C': 'learning', // vocabulary-learning
  'bsk_90F7A661AB776A5D0687D6BF8A30': 'learning', // thinking-partner
  'bsk_81AA7A4A50A371FE26519666ED65': 'learning', // language-learning
  'bsk_274C622882A6DDE479EA8174C31C': 'learning', // Learning
  'bsk_0E2B72DA5B5F8BB2CF80A92ED80C': 'learning', // yupi-skill
  'bsk_3A4CCB090C1C3CCFDEED53961124': 'learning', // socrates
};
