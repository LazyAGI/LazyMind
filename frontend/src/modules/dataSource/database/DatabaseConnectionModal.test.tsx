import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import DatabaseConnectionModal, {
  databaseConnectionToForm,
  parseDatabaseConnectionOptions,
} from "./DatabaseConnectionModal";
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
    options: { sslmode: "require" },
    is_verified: false,
    created_at: "2024-01-01T00:00:00.000Z",
    ...overrides,
  } as DatabaseConnectionItem;
}

describe("parseDatabaseConnectionOptions", () => {
  it("returns an empty object for blank input", () => {
    expect(parseDatabaseConnectionOptions()).toEqual({});
    expect(parseDatabaseConnectionOptions("   ")).toEqual({});
  });

  it("parses a valid JSON object and stringifies non-string values", () => {
    expect(parseDatabaseConnectionOptions('{"sslmode":"require","timeout":30}')).toEqual({
      sslmode: "require",
      timeout: "30",
    });
  });

  it("throws when the value is a JSON array", () => {
    expect(() => parseDatabaseConnectionOptions("[1,2,3]")).toThrow();
  });

  it("throws when the value is not valid JSON", () => {
    expect(() => parseDatabaseConnectionOptions("{not-json")).toThrow();
  });
});

describe("databaseConnectionToForm", () => {
  it("maps a connection record into form values with a blank password", () => {
    const form = databaseConnectionToForm(makeConnection());
    expect(form.password).toBe("");
    expect(form.display_name).toBe("My DB");
    expect(form.options_text).toBe(JSON.stringify({ sslmode: "require" }, null, 2));
  });

  it("uses an empty options_text when there are no options", () => {
    const form = databaseConnectionToForm(makeConnection({ options: {} }));
    expect(form.options_text).toBe("");
  });
});

describe("DatabaseConnectionModal", () => {
  it("renders create mode fields with default db type and port", () => {
    renderWithProviders(
      <DatabaseConnectionModal open onCancel={vi.fn()} onSubmit={vi.fn()} />,
    );
    expect(screen.getByText("admin.dataSourceDatabaseCreateTitle")).toBeInTheDocument();
  });

  it("renders edit mode with the edit title when editing a record", () => {
    renderWithProviders(
      <DatabaseConnectionModal
        open
        editing={makeConnection()}
        onCancel={vi.fn()}
        onSubmit={vi.fn()}
      />,
    );
    expect(screen.getByText("admin.dataSourceDatabaseEditTitle")).toBeInTheDocument();
    expect(screen.getByDisplayValue("My DB")).toBeInTheDocument();
  });

  it("submits a valid payload when required fields are filled", async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    renderWithProviders(
      <DatabaseConnectionModal open onCancel={vi.fn()} onSubmit={onSubmit} />,
    );

    fireEvent.change(screen.getByLabelText("admin.dataSourceDatabaseName"), {
      target: { value: "New DB" },
    });
    fireEvent.change(screen.getByLabelText("admin.dataSourceDatabaseHost"), {
      target: { value: "host.example.com" },
    });
    fireEvent.change(screen.getByLabelText("admin.dataSourceDatabaseDatabaseName"), {
      target: { value: "mydb" },
    });
    fireEvent.change(screen.getByLabelText("admin.dataSourceDatabaseUsername"), {
      target: { value: "admin" },
    });
    fireEvent.change(screen.getByLabelText("admin.dataSourceDatabasePassword"), {
      target: { value: "secret" },
    });

    fireEvent.click(screen.getByText("common.save"));

    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    expect(onSubmit.mock.calls[0][0]).toMatchObject({
      display_name: "New DB",
      host: "host.example.com",
      database_name: "mydb",
      username: "admin",
      password: "secret",
    });
  });

  it("does not call onSubmit when a required field is missing", async () => {
    // The modal's Modal.onOk fires handleOk() without awaiting it, so the rejected
    // validateFields() promise surfaces as an unhandled rejection; swallow it here.
    const rejectionHandler = () => {};
    process.on("unhandledRejection", rejectionHandler);

    const onSubmit = vi.fn();
    renderWithProviders(
      <DatabaseConnectionModal open onCancel={vi.fn()} onSubmit={onSubmit} />,
    );

    fireEvent.click(screen.getByText("common.save"));

    await waitFor(() => {
      expect(screen.getByText("admin.dataSourceDatabaseNameRequired")).toBeInTheDocument();
    });
    expect(onSubmit).not.toHaveBeenCalled();

    process.off("unhandledRejection", rejectionHandler);
  });
});
