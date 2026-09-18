import { describe, expect, it } from "vitest";

import {
	mapCloudConnectionToDataSourceConnection,
	mapCloudConnectionToNotionAccount,
} from "./dataSourceConnection";

describe("Provider connection method preservation", () => {
	it.each(["legacy_byo", "managed_oauth", "cli_personal_app"])(
	  "keeps %s so Cloud state cannot change the connection execution path",
	  (connectionMethod) => {
		const payload = {
		  connection_id: `connection-${connectionMethod}`,
		  connection_method: connectionMethod,
		  display_name: "Fixture account",
		  provider: "notion",
		  provider_account_meta: {},
		  status: "ACTIVE",
		} as never;

		expect(
		  mapCloudConnectionToDataSourceConnection(payload, "notion").connectionMethod,
		).toBe(connectionMethod);
		expect(mapCloudConnectionToNotionAccount(payload).connection?.connectionMethod).toBe(
		  connectionMethod,
		);
	  },
	);
});
