import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";

const listDatabaseConnectionsMock = vi.fn();
const createDatabaseConnectionMock = vi.fn();
const updateDatabaseConnectionMock = vi.fn();
const deleteDatabaseConnectionMock = vi.fn();
const checkDatabaseConnectionMock = vi.fn();

vi.mock("../api/databaseConnections", () => ({
  listDatabaseConnections: (...args: unknown[]) => listDatabaseConnectionsMock(...args),
  createDatabaseConnection: (...args: unknown[]) => createDatabaseConnectionMock(...args),
  updateDatabaseConnection: (...args: unknown[]) => updateDatabaseConnectionMock(...args),
  deleteDatabaseConnection: (...args: unknown[]) => deleteDatabaseConnectionMock(...args),
  checkDatabaseConnection: (...args: unknown[]) => checkDatabaseConnectionMock(...args),
}));

vi.mock("@/components/request", () => ({
  localizeErrorCode: (code?: string) => `localized-code:${code}`,
}));

import DatabaseConnectionsPage from "./index";
import type { DatabaseConnectionItem } from "../api/databaseConnections";

function makeConnection(overrides: Partial<DatabaseConnectionItem> = {}): DatabaseConnectionItem {
  return {
    id: "conn-1",
    display_name: "My DB",
    description: "Primary database",
    db_type: "postgresql",
    host: "db.example.com",
    port: 5432,
    database_name: "app",
    username: "root",
    options: {},
    is_verified: false,
    created_at: "2024-01-01T00:00:00.000Z",
    ...overrides,
  } as DatabaseConnectionItem;
}

describe("DatabaseConnectionsPage", () => {
  beforeEach(() => {
    listDatabaseConnectionsMock.mockReset();
    createDatabaseConnectionMock.mockReset();
    updateDatabaseConnectionMock.mockReset();
    deleteDatabaseConnectionMock.mockReset();
    checkDatabaseConnectionMock.mockReset();
  });

  it("loads and renders the list of database connections on mount", async () => {
    listDatabaseConnectionsMock.mockResolvedValue({ connections: [makeConnection()] });

    renderWithProviders(<DatabaseConnectionsPage />);

    await waitFor(() => {
      expect(screen.getByText("My DB")).toBeInTheDocument();
    });
    expect(listDatabaseConnectionsMock).toHaveBeenCalled();
  });

  it("shows an empty state title when there are no connections", async () => {
    listDatabaseConnectionsMock.mockResolvedValue({ connections: [] });

    renderWithProviders(<DatabaseConnectionsPage />);

    await waitFor(() => {
      expect(screen.getByText("admin.dataSourceDatabaseEmptyTitle")).toBeInTheDocument();
    });
  });

  it("opens the create modal when clicking the create action", async () => {
    listDatabaseConnectionsMock.mockResolvedValue({ connections: [] });

    renderWithProviders(<DatabaseConnectionsPage />);

    await waitFor(() => {
      expect(listDatabaseConnectionsMock).toHaveBeenCalled();
    });

    fireEvent.click(screen.getByText("admin.dataSourceDatabaseCreateAction"));

    expect(screen.getByText("admin.dataSourceDatabaseCreateTitle")).toBeInTheDocument();
  });

  it("opens the setup guide modal when clicking the guide action", async () => {
    listDatabaseConnectionsMock.mockResolvedValue({ connections: [] });

    renderWithProviders(<DatabaseConnectionsPage />);

    await waitFor(() => {
      expect(listDatabaseConnectionsMock).toHaveBeenCalled();
    });

    fireEvent.click(screen.getByText("admin.dataSourceDatabaseGuideAction"));

    expect(screen.getByText("admin.dataSourceDatabaseGuideTitle")).toBeInTheDocument();
  });

  it("checks a connection and shows a success message on success", async () => {
    listDatabaseConnectionsMock.mockResolvedValue({ connections: [makeConnection()] });
    checkDatabaseConnectionMock.mockResolvedValue({ success: true, table_count: 5, message: "ok" });
    const messageModule = await import("antd");
    const successSpy = vi.spyOn(messageModule.message, "success").mockImplementation(() => "" as any);

    renderWithProviders(<DatabaseConnectionsPage />);

    await waitFor(() => {
      expect(screen.getByText("My DB")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText("admin.dataSourceDatabaseTestAction"));

    await waitFor(() => {
      expect(checkDatabaseConnectionMock).toHaveBeenCalledWith("conn-1");
    });
    expect(successSpy).toHaveBeenCalledWith("admin.dataSourceDatabaseCheckSuccess");
    successSpy.mockRestore();
  });

  it("shows an error message when the connection check fails", async () => {
    listDatabaseConnectionsMock.mockResolvedValue({ connections: [makeConnection()] });
    checkDatabaseConnectionMock.mockResolvedValue({ success: false, message: "failed" });
    const messageModule = await import("antd");
    const errorSpy = vi.spyOn(messageModule.message, "error").mockImplementation(() => "" as any);

    renderWithProviders(<DatabaseConnectionsPage />);

    await waitFor(() => {
      expect(screen.getByText("My DB")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText("admin.dataSourceDatabaseTestAction"));

    await waitFor(() => {
      expect(errorSpy).toHaveBeenCalledWith("localized-code:2000509");
    });
    errorSpy.mockRestore();
  });
});
