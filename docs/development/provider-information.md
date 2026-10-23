# Supplier information pages

The supplier name and information icon in the right configuration header open a read-only information drawer. The left navigation keeps its selection and expansion behavior. The drawer has its own reading target: reading a sibling service does not switch the configured supplier. Only the explicit configuration action uses the existing unsaved-change guard.

`frontend/src/modules/modelProvider/providerInformation.ts` resolves localized editorial guides through the immutable brand/service helpers in `supplierServices.ts`. Custom suppliers and unknown identities use a generic guide, even when their name or request adapter matches a built-in supplier. Keep vendor-specific credentials, product scope and official references in this editorial layer. Do not infer them from the request protocol, user-editable API address or inherited template description.

The reading component is `components/ProviderInformationDrawer.tsx`; interface labels are in `providerInformationCopy.ts`. Current saved API addresses come from workspace state. They are explicitly labelled as saved addresses, not official defaults. Draft inputs and API keys are not passed to the drawer. Opening or reading the guide does not call a supplier or modify settings.

## Maintaining guides

- Cover both Chinese and English for each service. Keep steps specific to the service; self-hosted services need deployment reachability and authentication guidance rather than a cloud subscription tutorial.
- Verify claims against official documentation and include named HTTPS references. Update the guide's verification date only after checking its references and product-specific claims.
- Keep each plan's scope distinct. A compatible-tools statement for one subscription is not evidence for another subscription, including plans under the same brand.
- Do not copy prices, fixed quotas, model totals or a second endpoint/protocol registry into guides. Link to the official source and use existing canonical configuration metadata when technical values are necessary.
- A vendor's documented capabilities are not proof that LazyMind's current adapter implements them, nor that an account can call a model. Preserve that distinction in copy.

Run `pnpm exec vitest run src/modules/modelProvider/providerInformation.test.ts src/modules/modelProvider/components/ProviderInformationDrawer.test.tsx src/modules/modelProvider/components/ProviderWorkspace.test.tsx` from `frontend/`. Also inspect long service names, mobile layout, keyboard focus, custom branding and draft preservation in a real browser when changing the reading interface.
