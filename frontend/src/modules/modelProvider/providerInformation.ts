import { getSupplierBrandKey, resolveSupplierService, type SupplierServiceIdentity } from "./supplierServices";
import { genericInformation, providerInformationContent, type InformationText } from "./providerInformationContent";
import { programmingScopeLink } from "./supplierNavigation";
import { getProviderProfileContent, type ProviderProfile } from "./providerProfileContent";

export interface ProviderInformationGuide {
  kind: "cloud" | "plan" | "self-hosted" | "custom";
  profile?: ProviderProfile;
  summary: string;
  audience: string;
  credential: string;
  endpointNote: string;
  scope?: { tone: "info" | "warning"; text: string };
  steps: { title: string; body: string }[];
  faqs: { question: string; answer: string }[];
  links: { label: string; url: string }[];
  verifiedAt?: string;
}

/** Editorial information follows catalog identity, never the mutable request adapter. */
export function getProviderInformation(provider: SupplierServiceIdentity, language = "en-US"): ProviderInformationGuide {
  const text = (value: InformationText) => value[/^zh(?:[-_]|$)/i.test(language) ? 0 : 1];
  const isCustom = provider.isCustom === true || provider.is_custom === true;
  const service = resolveSupplierService(provider);
  const hasExplicitService = Boolean((provider.serviceKey || provider.service_key || "").trim());
  const brand = !isCustom && provider.name.trim() ? getSupplierBrandKey(provider) : undefined;
  const identity = isCustom ? undefined : service ? `${service.brandKey}:${service.serviceKey}` : hasExplicitService ? undefined : brand;
  const content = identity && Object.prototype.hasOwnProperty.call(providerInformationContent, identity)
    ? providerInformationContent[identity]
    : genericInformation;
  const profile = content !== genericInformation
    ? getProviderProfileContent(service?.brandKey || brand || "", service?.serviceKey, language)
    : undefined;
  const links = content.links.map(([label, url]) => ({ label: text(label), url }));
  const scopeUrl = content !== genericInformation ? programmingScopeLink(provider) : "";
  if (scopeUrl && !links.some(({ url }) => url === scopeUrl)) {
    links.push({ label: text(["使用范围与工具接入", "Usage scope and tool integration"]), url: scopeUrl });
  }
  return {
    kind: content.kind,
    ...(profile ? { profile } : {}),
    summary: text(content.summary),
    audience: text(content.audience),
    credential: text(content.credential),
    endpointNote: text(content.endpointNote),
    ...(content.scope ? { scope: { tone: content.scope.tone, text: text(content.scope.text) } } : {}),
    steps: [
      { title: text(["准备服务", "Prepare the service"]), body: text(content.prepare) },
      { title: text(["填写连接配置", "Set up the connection"]), body: text(content.connect) },
      { title: text(["测试具体模型", "Test a specific model"]), body: text([
        "返回配置页后保存连接配置，获取模型列表或手动添加准确的模型 ID，再测试准备使用的模型。",
        "Return to configuration to save the connection, fetch the model list or manually add the exact model ID, then test the model you intend to use.",
      ]) },
    ],
    faqs: [
      ...content.faqs.map(([question, answer]) => ({ question: text(question), answer: text(answer) })),
      { question: text(["列表里有模型，就一定能调用吗？", "Does a listed model guarantee access?"]), answer: text([
        "不一定。目录用于查找模型；能否调用还取决于账号权限、服务或部署状态，以及该模型在当前配置下的实际测试结果。",
        "No. The catalog helps you find models. Access still depends on account permissions, service or deployment status, and an actual test with the current configuration.",
      ]) },
    ],
    links,
    ...(content !== genericInformation ? { verifiedAt: "2026-10-23" } : {}),
  };
}
