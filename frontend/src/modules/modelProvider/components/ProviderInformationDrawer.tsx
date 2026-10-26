import { useEffect, useId, useMemo, useRef, useState } from "react";
import { CheckOutlined, ExportOutlined, InfoCircleOutlined, RightOutlined } from "@ant-design/icons";
import { Button, Collapse, Drawer, Tooltip } from "antd";
import { useTranslation } from "react-i18next";
import { isCustomLogoDataUrl } from "../customLogo";
import { useFocusModality } from "../hooks/useFocusModality";
import { getProviderLogoUrl } from "../providerBranding";
import { getProviderDisplayName, getProviderServiceDisplayName } from "../providerDisplayNames";
import { getProviderInformation } from "../providerInformation";
import { getProviderProtocols } from "../providerProtocols";
import { getProviderInformationCopy } from "../providerInformationCopy";
import { resolveSupplierService } from "../supplierServices";
import type { AddedProvider } from "../pages/ModelProvidersPage";
import "./ProviderInformationDrawer.scss";

interface ProviderInformationDrawerProps {
  open: boolean;
  provider: AddedProvider;
  providers: AddedProvider[];
  onClose(): void;
  onConfigure(providerId: string): void;
  onAfterClose?(): void;
}

export function ProviderInformationDrawer({ open, provider, providers, onClose, onConfigure, onAfterClose }: ProviderInformationDrawerProps) {
  const { i18n } = useTranslation();
  const language = i18n?.resolvedLanguage || i18n?.language;
  const copy = getProviderInformationCopy(language);
  const focusModality = useFocusModality();
  const sectionId = useId();
  const [readingId, setReadingId] = useState(provider.id);
  const [helpOpen, setHelpOpen] = useState(false);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const pendingReadingFocus = useRef(false);

  useEffect(() => {
    if (open) setReadingId(provider.id);
  }, [open, provider.id]);

  const siblings = useMemo(() => {
    const configuredService = resolveSupplierService(provider);
    if (!configuredService) return [];
    return providers
      .map((item) => item.id === provider.id ? provider : item)
      .filter((item) => resolveSupplierService(item)?.brandKey === configuredService.brandKey)
      .sort((left, right) => (resolveSupplierService(left)?.order ?? 0) - (resolveSupplierService(right)?.order ?? 0));
  }, [provider, providers]);

  const readingProvider = readingId === provider.id
    ? provider
    : siblings.find((item) => item.id === readingId) || provider;

  useEffect(() => {
    if (readingId !== readingProvider.id) setReadingId(readingProvider.id);
  }, [readingId, readingProvider.id]);

  useEffect(() => {
    setHelpOpen(false);
  }, [open, readingProvider.id]);

  useEffect(() => {
    if (!open) return;
    if (pendingReadingFocus.current) {
      pendingReadingFocus.current = false;
      headingRef.current?.focus({ preventScroll: true });
    }
  }, [open, readingProvider.id]);

  const readService = (providerId: string) => {
    if (providerId === readingProvider.id) {
      headingRef.current?.focus({ preventScroll: true });
    } else {
      pendingReadingFocus.current = true;
      setReadingId(providerId);
    }
  };

  const guide = getProviderInformation(readingProvider, language);
  const protocols = getProviderProtocols(readingProvider, language);
  const name = getProviderDisplayName(readingProvider, language);
  const resourceCounts = new Map<string, number>();
  for (const link of guide.links) resourceCounts.set(link.kind, (resourceCounts.get(link.kind) || 0) + 1);
  const fallbackLogo = getProviderLogoUrl(
    readingProvider.brandKey || readingProvider.name || readingProvider.protocolName || readingProvider.source,
    readingProvider.isCustom,
  ) || "/provider-icons/custom.svg";
  const logo = isCustomLogoDataUrl(readingProvider.logoDataUrl) ? readingProvider.logoDataUrl : fallbackLogo;

  return <Drawer
    rootClassName={`model-provider-focus-${focusModality}`}
    className="provider-information-drawer"
    title={<div className="provider-information-heading">
      <span className="provider-information-logo" aria-hidden="true">
        <img src={logo} alt="" onError={(event) => {
          if (event.currentTarget.getAttribute("src") !== fallbackLogo) event.currentTarget.src = fallbackLogo;
        }} />
      </span>
      <div><h2 ref={headingRef} tabIndex={-1}>{name}</h2></div>
    </div>}
    open={open}
    width="min(720px, 100vw)"
    placement="right"
    onClose={onClose}
    afterOpenChange={(visible) => { if (!visible) onAfterClose?.(); }}
    footer={<div className="provider-information-footer">
      <Button onClick={onClose}>{copy.returnToConfiguration}</Button>
      {readingProvider.id !== provider.id ? <Button type="primary" onClick={() => onConfigure(readingProvider.id)}>{copy.configure}</Button> : null}
    </div>}
  >
    <article className="provider-information-content">
      <header className="provider-information-introduction">
        <p className="provider-information-summary">{guide.profile?.introduction || guide.summary}</p>
      </header>

      {guide.profile?.highlights.length ? <section className="provider-information-section provider-information-highlights" aria-labelledby={`${sectionId}-highlights`}>
        <h3 id={`${sectionId}-highlights`} className="provider-information-visually-hidden">{copy.highlights}</h3>
        <ul>{guide.profile.highlights.map((highlight) => <li key={highlight}>{highlight}</li>)}</ul>
      </section> : null}

      {guide.links.length || guide.profile?.websiteUrl ? <section className="provider-information-section provider-information-sources" aria-labelledby={`${sectionId}-sources`}>
        <h3 id={`${sectionId}-sources`} className="provider-information-visually-hidden">{copy.sources}</h3>
        <ul>
          {guide.profile?.websiteUrl ? <li className="provider-information-website-item">
            <a className="provider-information-website" href={guide.profile.websiteUrl}
              target="_blank" rel="noopener noreferrer" title={`${guide.profile.websiteUrl} · ${copy.newTab}`}
              aria-label={`${copy.website}: ${guide.profile.websiteUrl}`}>
              <span>{copy.website}</span>
              <span className="provider-information-website-host">{new URL(guide.profile.websiteUrl).hostname.replace(/^www\./, "")}</span>
              <ExportOutlined aria-hidden="true" />
            </a>
          </li> : null}
          {guide.links.map((link) => {
            const label = resourceCounts.get(link.kind) === 1 ? copy.resources[link.kind] : link.label;
            return <li key={link.url}>
              <a href={link.url} target="_blank" rel="noopener noreferrer" title={`${link.label} · ${copy.newTab}`} aria-label={label === link.label ? link.label : `${label}: ${link.label}`}>
                {label}<ExportOutlined aria-hidden="true" />
              </a>
            </li>;
          })}
        </ul>
      </section> : null}

      {protocols.status === "documented" ? <section className="provider-information-section provider-information-protocols" aria-label={copy.protocols}>
        <h3>{copy.protocols}</h3>
        <ul aria-label={copy.protocols}>{protocols.protocols.map(protocol => <li key={protocol.id}>{protocol.label}</li>)}</ul>
        <p className="provider-information-note">{copy.protocolScope}</p>
        <details><summary>{copy.sources}</summary><p>{protocols.note}</p>
          {protocols.sources.map(source => <a key={source.url} href={source.url} target="_blank" rel="noopener noreferrer">{source.label}<ExportOutlined aria-hidden="true" /></a>)}
        </details>
      </section> : null}

      {siblings.length > 1 ? <section className="provider-information-section" aria-labelledby={`${sectionId}-compare`}>
        <div className="provider-information-section-title">
          <h3 id={`${sectionId}-compare`}>{copy.compare}</h3>
          <p className="provider-information-note provider-information-compare-hint">{copy.compareHint}</p>
        </div>
        <table className="provider-information-comparison">
          <thead><tr><th scope="col">{copy.service}</th><th scope="col">{copy.purpose}</th></tr></thead>
          <tbody>{siblings.map((sibling) => {
            const information = getProviderInformation(sibling, language);
            return <tr key={sibling.id} className={sibling.id === readingProvider.id ? "is-reading" : undefined}>
              <th scope="row"><div className="provider-information-service-name">
                <button type="button" aria-label={copy.readService(getProviderDisplayName(sibling, language))}
                  aria-current={sibling.id === readingProvider.id ? "page" : undefined}
                  onClick={() => readService(sibling.id)}>
                  {getProviderServiceDisplayName(sibling, language)}
                </button>
                {sibling.id === provider.id ? <Tooltip title={copy.currentlyEditing} trigger={["hover", "focus"]}>
                  <span className="provider-information-current" role="img" aria-label={copy.currentlyEditing} tabIndex={0}>
                    <CheckOutlined aria-hidden="true" />
                  </span>
                </Tooltip> : null}
              </div></th>
              <td data-label={copy.purpose}>{information.profile?.serviceSummary || information.summary}</td>
            </tr>;
          })}</tbody>
        </table>
      </section> : null}

      <section className="provider-information-section provider-information-help">
        <Collapse ghost activeKey={helpOpen ? ["connection"] : []} onChange={(keys) => setHelpOpen(keys.includes("connection"))}
          destroyOnHidden expandIconPosition="end"
          expandIcon={({ isActive }) => <RightOutlined rotate={isActive ? 90 : 0} aria-hidden="true" />} items={[{
            key: "connection",
            label: <h3>{copy.connectionHelp}</h3>,
            children: <div className="provider-information-help-content">
              <dl className="provider-information-facts">
                <div><dt>{copy.credential}</dt><dd>{guide.credential}</dd></div>
              </dl>
              {guide.scope ? <aside className={`provider-information-scope provider-information-scope-${guide.scope.tone}`} aria-label={copy.scope}>
                <InfoCircleOutlined aria-hidden="true" />
                <div><strong>{copy.scope}</strong><p>{guide.scope.text}</p></div>
              </aside> : null}

              <section className="provider-information-section" aria-labelledby={`${sectionId}-setup`}>
                <h4 id={`${sectionId}-setup`}>{copy.setup}</h4>
                <ol className="provider-information-steps">
                  {guide.steps.map((step, index) => <li key={`${readingProvider.id}-${index}`}>
                    <span className="provider-information-step-number" aria-hidden="true">{index + 1}</span>
                    <div><h5>{step.title}</h5><p>{step.body}</p></div>
                  </li>)}
                </ol>
              </section>

              <section className="provider-information-section" aria-labelledby={`${sectionId}-address`}>
                <h4 id={`${sectionId}-address`}>{copy.savedAddress}</h4>
                <div className="provider-information-address">{readingProvider.baseUrl
                  ? <code>{readingProvider.baseUrl}</code>
                  : <span>{copy.noSavedAddress}</span>}</div>
                <p className="provider-information-note">{guide.endpointNote}</p>
              </section>

              {guide.faqs.length ? <section className="provider-information-section provider-information-faq" aria-labelledby={`${sectionId}-faq`}>
                <h4 id={`${sectionId}-faq`}>{copy.faq}</h4>
                <Collapse key={readingProvider.id} ghost accordion expandIconPosition="end" items={guide.faqs.map((faq, index) => ({
                  key: String(index), label: faq.question, children: <p>{faq.answer}</p>,
                }))} />
              </section> : null}
            </div>,
          }]} />
      </section>
    </article>
  </Drawer>;
}

export default ProviderInformationDrawer;
