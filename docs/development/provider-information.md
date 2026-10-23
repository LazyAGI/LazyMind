# Supplier information pages

The supplier name and information icon in the right configuration header open a read-only information drawer. The left navigation keeps its selection and expansion behavior. The drawer has its own reading target: reading a sibling service does not switch the configured supplier. Only the explicit configuration action uses the existing unsaved-change guard.

`frontend/src/modules/modelProvider/providerInformation.ts` resolves localized profiles and secondary connection guides through the immutable brand/service helpers in `supplierServices.ts`. `providerProfileContent.ts` maintains actual supplier/product introductions, official websites and product characteristics. These describe the supplier, rather than steps for configuring LazyMind. Custom suppliers and unknown identities use a generic guide, even when their name or request adapter matches a built-in supplier. Do not infer vendor information from the request protocol, user-editable API address or inherited template description.

The reading component is `components/ProviderInformationDrawer.tsx`; interface labels are in `providerInformationCopy.ts`. The overview contains a short introduction, two distinct product facts, compact official links and a comparison of related services. Each profile's `serviceSummary` describes its purpose and differences in one sentence instead of repeating its introduction. Connection steps, full usage scope, saved API address and product-specific FAQs are secondary help, collapsed by default. Saved API addresses come from workspace state and are explicitly labelled as saved addresses, never official websites or defaults. Draft inputs and API keys are not passed to the drawer. Opening or reading the page does not call a supplier or modify settings.

## Maintaining guides

- Cover both Chinese and English for each service. Introduce the actual company, platform, project or subscription product, and use its official brand website separately from its API endpoint. Self-hosted services describe their inference project; they do not inherit a cloud supplier's introduction.
- Keep the introduction, two highlights and `serviceSummary` complementary. Highlights should add concrete differences such as credentials, product scope or deployment behavior. Put the complete explanation in connection help instead of duplicating it across sections.
- Give every official resource an explicit `kind` (`docs`, `credentials`, `models`, `pricing`, `scope`, `product`, `deployment` or `faq`). The drawer displays short labels while preserving full labels in accessible names and tooltips. Multiple links with the same kind retain their full visible labels so readers can distinguish them. Authentication documentation can be a credentials resource without being a key-creation page; combined model-and-pricing pages are not separate public price lists.
- Verify claims against official documentation and include named HTTPS references. Update the guide's verification date only after checking its references and product-specific claims.
- Keep each plan's scope distinct. A compatible-tools statement for one subscription is not evidence for another subscription, including plans under the same brand.
- Do not copy prices, fixed quotas, model totals or a second endpoint/protocol registry into guides. Link to the official source and use existing canonical configuration metadata when technical values are necessary.
- A vendor's documented capabilities are not proof that LazyMind's current adapter implements them, nor that an account can call a model. Preserve that distinction in copy.
- Explain account access once in the final setup step; reserve FAQs for service-specific questions rather than appending the same generic answer to every guide.

Run `pnpm exec vitest run src/modules/modelProvider/providerInformation.test.ts src/modules/modelProvider/providerProfileContent.test.ts src/modules/modelProvider/components/ProviderInformationDrawer.test.tsx src/modules/modelProvider/components/ProviderWorkspace.test.tsx` from `frontend/`. Also inspect long service names, mobile layout, keyboard focus, custom branding and draft preservation in a real browser when changing the reading interface.

## Reference patterns

Reviewed on 2026-10-23. [LobeHub's provider card type](https://github.com/lobehub/lobehub/blob/canary/packages/model-bank/src/types/providerCard.ts) separates the supplier description and website from API-key and model links. [Cherry Studio's provider schema](https://github.com/CherryHQ/cherry-studio/blob/main/packages/provider-registry/src/schemas/provider.ts) distinguishes official, documentation, API-key, model and billing resources. Keep resource labels accurate: an account's billing page is not a public price list.
