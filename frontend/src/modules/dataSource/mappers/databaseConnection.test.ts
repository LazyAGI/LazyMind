import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import { mapDatabaseConnectionToDataSource } from "./databaseConnection";
import type { DatabaseConnectionItem } from "../api/databaseConnections";

const t = ((key: string) => key) as unknown as TFunction;

function buildConnection(overrides: Partial<DatabaseConnectionItem> = {}) {
  return {
    id: "db-1",
    display_name: "My DB",
    database_name: "mydb",
    host: "localhost",
    port: 5432,
    is_verified: true,
    description: "",
    update_time: "2026-01-01T00:00:00Z",
    create_time: "2025-12-01T00:00:00Z",
    ...overrides,
  } as DatabaseConnectionItem;
}

describe("mapDatabaseConnectionToDataSource", () => {
  it("maps a verified connection to an active/connected data source", () => {
    const result = mapDatabaseConnectionToDataSource(buildConnection(), t);
    expect(result.status).toBe("active");
    expect(result.connectionState).toBe("connected");
    expect(result.enabled).toBe(true);
    expect(result.target).toBe("localhost:5432/mydb");
    expect(result.id).toBe("database:db-1");
    expect(result.warning).toBeUndefined();
  });

  it("marks an unverified connection with an error as errored", () => {
    const result = mapDatabaseConnectionToDataSource(
      buildConnection({ is_verified: false, last_check_error: "timeout" }),
      t,
    );
    expect(result.status).toBe("error");
    expect(result.connectionState).toBe("error");
    expect(result.enabled).toBe(false);
    expect(result.warning).toBeTruthy();
    expect(result.logs[0].result).toBe("failed");
  });

  it("marks an unverified connection without an error as pending", () => {
    const result = mapDatabaseConnectionToDataSource(
      buildConnection({ is_verified: false }),
      t,
    );
    expect(result.connectionState).toBe("pending");
    expect(result.logs[0].result).toBe("warning");
  });

  it("falls back to database_name and the composed address for missing fields", () => {
    const result = mapDatabaseConnectionToDataSource(
      buildConnection({ display_name: undefined, description: undefined }),
      t,
    );
    expect(result.name).toBe("mydb");
    expect(result.description).toBe("localhost:5432/mydb");
  });
});
