import { useEffect, useId, useMemo, useRef, useState } from "react";
import { ExportOutlined, InfoCircleOutlined } from "@ant-design/icons";
import { Button, Collapse, Drawer } from "antd";
import { useTranslation } from "react-i18next";
import { isCustomLogoDataUrl } from "../customLogo";
import { useFocusModality } from "../hooks/useFocusModality";
import { getProviderLogoUrl } from "../providerBranding";
import { getProviderDisplayName, getProviderServiceDisplayName } from "../providerDisplayNames";
import { getProviderInformation } from "../providerInformation";
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
  const contentRef = useRef<HTMLElement>(null);
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

  const resetReadingScroll = () => {
    const body = contentRef.current?.parentElement;
    if (body) body.scrollTop = 0;
  };

  useEffect(() => {
    if (!open) return;
    resetReadingScroll();
    if (pendingReadingFocus.current) {
      pendingReadingFocus.current = false;
      headingRef.current?.focus({ preventScroll: true });
    }
  }, [open, readingProvider.id]);

  const readService = (providerId: string) => {
    if (providerId === readingProvider.id) {
      resetReadingScroll();
      headingRef.current?.focus({ preventScroll: true });
    } else {
      pendingReadingFocus.current = true;
      setReadingId(providerId);
    }
  };

  const guide = getProviderInformation(readingProvider, language);
  const name = getProviderDisplayName(readingProvider, language);
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
      <div><h2 ref={headingRef} tabIndex={-1}>{name}</h2><span className="provider-information-title-separator"> · </span><p>{copy.title}</p></div>
    </div>}
    open={open}
    width="min(720px, 100vw)"
    placement="right"
    onClose={onClose}
    afterOpenChange={(visible) => { if (visible) resetReadingScroll(); else onAfterClose?.(); }}
    footer={<div className="provider-information-footer">
      <Button onClick={onClose}>{copy.returnToConfiguration}</Button>
      {readingProvider.id !== provider.id ? <Button type="primary" onClick={() => onConfigure(readingProvider.id)}>{copy.configure}</Button> : null}
    </div>}
  >
    <article ref={contentRef} className="provider-information-content">
      <header className="provider-information-introduction">
        <p className="provider-information-summary">{guide.summary}</p>
        <dl className="provider-information-facts">
          <div><dt>{copy.audience}</dt><dd>{guide.audience}</dd></div>
          <div><dt>{copy.credential}</dt><dd>{guide.credential}</dd></div>
        </dl>
        {guide.scope ? <aside className={`provider-information-scope provider-information-scope-${guide.scope.tone}`} aria-label={copy.scope}>
          <InfoCircleOutlined aria-hidden="true" />
          <div><strong>{copy.scope}</strong><p>{guide.scope.text}</p></div>
        </aside> : null}
      </header>

      <section className="provider-information-section" aria-labelledby={`${sectionId}-setup`}>
        <h3 id={`${sectionId}-setup`}>{copy.setup}</h3>
        <ol className="provider-information-steps">
          {guide.steps.map((step, index) => <li key={`${readingProvider.id}-${index}`}>
            <span className="provider-information-step-number" aria-hidden="true">{index + 1}</span>
            <div><h4>{step.title}</h4><p>{step.body}</p></div>
          </li>)}
        </ol>
      </section>

      <section className="provider-information-section" aria-labelledby={`${sectionId}-address`}>
        <h3 id={`${sectionId}-address`}>{copy.savedAddress}</h3>
        <div className="provider-information-address">{readingProvider.baseUrl
          ? <code>{readingProvider.baseUrl}</code>
          : <span>{copy.noSavedAddress}</span>}</div>
        <p className="provider-information-note">{guide.endpointNote}</p>
      </section>

      {siblings.length > 1 ? <section className="provider-information-section" aria-labelledby={`${sectionId}-compare`}>
        <h3 id={`${sectionId}-compare`}>{copy.compare}</h3>
        <p className="provider-information-note provider-information-compare-hint">{copy.compareHint}</p>
        <table className="provider-information-comparison">
          <thead><tr><th scope="col">{copy.service}</th><th scope="col">{copy.purpose}</th><th scope="col">{copy.credential}</th></tr></thead>
          <tbody>{siblings.map((sibling) => {
            const information = getProviderInformation(sibling, language);
            return <tr key={sibling.id} className={sibling.id === readingProvider.id ? "is-reading" : undefined}>
              <th scope="row">
                <button type="button" aria-label={copy.readService(getProviderDisplayName(sibling, language))}
                  aria-current={sibling.id === readingProvider.id ? "page" : undefined}
                  onClick={() => readService(sibling.id)}>
                  {getProviderServiceDisplayName(sibling, language)}
                </button>
                {sibling.id === provider.id ? <span className="provider-information-current">{copy.currentlyConfigured}</span> : null}
              </th>
              <td data-label={copy.purpose}>{information.audience}</td>
              <td data-label={copy.credential}>{information.credential}</td>
            </tr>;
          })}</tbody>
        </table>
      </section> : null}

      {guide.faqs.length ? <section className="provider-information-section provider-information-faq" aria-labelledby={`${sectionId}-faq`}>
        <h3 id={`${sectionId}-faq`}>{copy.faq}</h3>
        <Collapse key={readingProvider.id} ghost accordion expandIconPosition="end" items={guide.faqs.map((faq, index) => ({
          key: String(index), label: faq.question, children: <p>{faq.answer}</p>,
        }))} />
      </section> : null}

      {guide.links.length ? <section className="provider-information-section provider-information-sources" aria-labelledby={`${sectionId}-sources`}>
        <h3 id={`${sectionId}-sources`}>{copy.sources}</h3>
        <ul>{guide.links.map((link) => <li key={link.url}>
          <a href={link.url} target="_blank" rel="noopener noreferrer" title={copy.newTab}>
            {link.label}<ExportOutlined aria-hidden="true" />
          </a>
        </li>)}</ul>
        {guide.verifiedAt ? <p className="provider-information-verified">{copy.verifiedAt(guide.verifiedAt)}</p> : null}
      </section> : null}
    </article>
  </Drawer>;
}

export default ProviderInformationDrawer;
