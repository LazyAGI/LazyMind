import { getSupplierBrandKey, resolveSupplierService, type SupplierServiceIdentity } from "./supplierServices";
import { genericInformation, providerInformationContent, type InformationText } from "./providerInformationContent";
import { programmingScopeLink } from "./supplierNavigation";
import { getProviderProfileContent, type ProviderProfile } from "./providerProfileContent";

export type ProviderInformationResourceKind = "docs" | "credentials" | "models" | "pricing" | "scope" | "product" | "deployment" | "faq";

export interface ProviderInformationResource {
  label: string;
  url: string;
  kind: ProviderInformationResourceKind;
}

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
  links: ProviderInformationResource[];
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
  const links = content.links.map(([label, url, kind]) => ({ label: text(label), url, kind }));
  const scopeUrl = content !== genericInformation ? programmingScopeLink(provider) : "";
  if (scopeUrl && !links.some(({ url }) => url === scopeUrl)) {
    links.push({ label: text(["使用范围与工具接入", "Usage scope and tool integration"]), url: scopeUrl, kind: "scope" });
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
        "保存连接后同步或手动添加模型，再测试实际要用的模型；目录收录不代表当前账号可调用。",
        "Save the connection, sync or manually add models, then test the model you intend to use; a catalog listing does not establish access for your account.",
      ]) },
    ],
    faqs: content.faqs.map(([question, answer]) => ({ question: text(question), answer: text(answer) })),
    links,
    ...(content !== genericInformation ? { verifiedAt: "2026-10-23" } : {}),
  };
}
