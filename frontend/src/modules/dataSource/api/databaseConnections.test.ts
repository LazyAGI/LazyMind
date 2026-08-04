import { beforeEach, describe, expect, it, vi } from "vitest";

const getMock = vi.fn();
const postMock = vi.fn();
const patchMock = vi.fn();
const deleteMock = vi.fn();

vi.mock("@/components/request", () => ({
  BASE_URL: "https://example.com",
  axiosInstance: {
    get: (...args: unknown[]) => getMock(...args),
    post: (...args: unknown[]) => postMock(...args),
    patch: (...args: unknown[]) => patchMock(...args),
    delete: (...args: unknown[]) => deleteMock(...args),
  },
}));

import {
  checkDatabaseConnection,
  createDatabaseConnection,
  deleteDatabaseConnection,
  listDatabaseConnections,
  updateDatabaseConnection,
} from "./databaseConnections";

describe("databaseConnections api", () => {
  beforeEach(() => {
    getMock.mockReset();
    postMock.mockReset();
    patchMock.mockReset();
    deleteMock.mockReset();
  });

  it("lists database connections and unwraps the response", async () => {
    getMock.mockResolvedValue({ data: { data: { connections: [] } } });
    const result = await listDatabaseConnections();
    expect(getMock).toHaveBeenCalledWith(
      "https://example.com/api/core/data-sources/database-connections",
    );
    expect(result).toEqual({ connections: [] });
  });

  it("creates a database connection with the given payload", async () => {
    const payload = {
      display_name: "My DB",
      db_type: "mysql" as const,
      host: "localhost",
      database_name: "app",
      username: "root",
    };
    postMock.mockResolvedValue({ data: { data: { id: "conn-1", ...payload } } });
    const result = await createDatabaseConnection(payload);
    expect(postMock).toHaveBeenCalledWith(
      "https://example.com/api/core/data-sources/database-connections",
      payload,
    );
    expect(result).toMatchObject({ id: "conn-1" });
  });

  it("updates a database connection by id, url-encoding the id", async () => {
    patchMock.mockResolvedValue({ data: { data: { id: "conn a/1" } } });
    await updateDatabaseConnection("conn a/1", { display_name: "Renamed" });
    expect(patchMock).toHaveBeenCalledWith(
      "https://example.com/api/core/data-sources/database-connections/conn%20a%2F1",
      { display_name: "Renamed" },
    );
  });

  it("deletes a database connection by id", async () => {
    deleteMock.mockResolvedValue({ data: { data: { deleted: true } } });
    const result = await deleteDatabaseConnection("conn-1");
    expect(deleteMock).toHaveBeenCalledWith(
      "https://example.com/api/core/data-sources/database-connections/conn-1",
    );
    expect(result).toEqual({ deleted: true });
  });

  it("checks a database connection and returns table metadata", async () => {
    postMock.mockResolvedValue({
      data: { data: { success: true, message: "ok", table_count: 3, tables: ["a", "b", "c"] } },
    });
    const result = await checkDatabaseConnection("conn-1");
    expect(postMock).toHaveBeenCalledWith(
      "https://example.com/api/core/data-sources/database-connections/conn-1:check",
    );
    expect(result.table_count).toBe(3);
  });

  it("propagates request errors for create failures", async () => {
    postMock.mockRejectedValue(new Error("bad request"));
    await expect(
      createDatabaseConnection({
        display_name: "X",
        db_type: "postgresql",
        host: "h",
        database_name: "d",
        username: "u",
      }),
    ).rejects.toThrow("bad request");
  });
});
