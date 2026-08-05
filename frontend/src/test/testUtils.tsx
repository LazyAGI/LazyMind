import type { ReactElement, ReactNode } from "react";
import { render, type RenderOptions } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { I18nextProvider } from "react-i18next";
import i18n from "i18next";
import { initReactI18next } from "react-i18next";

// A minimal, synchronous i18next instance for tests. Keys resolve to
// themselves so assertions can match on translation keys directly.
export const testI18n = i18n.createInstance();
testI18n.use(initReactI18next).init({
  lng: "zh-CN",
  fallbackLng: "zh-CN",
  resources: { "zh-CN": { translation: {} } },
  interpolation: { escapeValue: false },
  parseMissingKeyHandler: (key) => key,
  react: { useSuspense: false },
});

interface WrapperOptions {
  route?: string;
}

function AllProviders({
  children,
  route = "/",
}: {
  children: ReactNode;
  route?: string;
}) {
  return (
    <I18nextProvider i18n={testI18n}>
      <MemoryRouter initialEntries={[route]}>{children}</MemoryRouter>
    </I18nextProvider>
  );
}

export function renderWithProviders(
  ui: ReactElement,
  options?: RenderOptions & WrapperOptions,
) {
  const { route, ...renderOptions } = options || {};
  return render(ui, {
    wrapper: ({ children }) => (
      <AllProviders route={route}>{children}</AllProviders>
    ),
    ...renderOptions,
  });
}

export * from "@testing-library/react";
