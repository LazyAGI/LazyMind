const english = {
  open: "View service information",
  title: "Service information",
  audience: "Best for",
  credential: "Credentials",
  scope: "Usage scope",
  setup: "Get started",
  savedAddress: "Saved API address",
  noSavedAddress: "No API address saved",
  compare: "Services from this provider",
  compareHint: "Read another service's guide here. Choose Configure this service when you are ready to switch.",
  service: "Service",
  purpose: "Purpose",
  currentlyConfigured: "Current configuration",
  faq: "Common questions",
  sources: "Official sources",
  verifiedAt: (date: string) => `Sources checked ${date}`,
  newTab: "Opens in a new tab",
  returnToConfiguration: "Return to configuration",
  configure: "Configure this service",
  readService: (name: string) => `Read ${name}`,
};

const chinese: typeof english = {
  open: "查看服务说明",
  title: "服务说明",
  audience: "适用场景",
  credential: "所需凭证",
  scope: "使用范围",
  setup: "开始使用",
  savedAddress: "已保存的API地址",
  noSavedAddress: "尚未保存API地址",
  compare: "同一供应商的服务",
  compareHint: "可在此阅读其他服务的说明，准备切换时再选择「配置此服务」。",
  service: "服务",
  purpose: "用途",
  currentlyConfigured: "当前配置",
  faq: "常见问题",
  sources: "官方资料",
  verifiedAt: (date: string) => `资料核验于 ${date}`,
  newTab: "在新标签页中打开",
  returnToConfiguration: "返回配置",
  configure: "配置此服务",
  readService: (name: string) => `查看${name}说明`,
};

export function getProviderInformationCopy(language = "en-US") {
  return /^zh(?:[-_]|$)/i.test(language) ? chinese : english;
}
