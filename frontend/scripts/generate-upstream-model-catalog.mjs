import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const script = fileURLToPath(new URL("./generate_upstream_model_catalog.py", import.meta.url));
const configuredPython = process.env.LAZYMIND_CATALOG_PYTHON;
// Windows desktop prerequisites already include uv; a python3 command is not
// normally available there. Unix image builds install Python directly.
const command = configuredPython || (process.platform === "win32" ? "uv" : "python3");
const prefix = !configuredPython && process.platform === "win32"
  ? ["run", "--no-project", "--python", "3.11.15", "python"]
  : [];
const result = spawnSync(command, [...prefix, script, ...process.argv.slice(2)], { stdio: "inherit" });
if (result.error) console.error(`Cannot prepare model catalog: ${result.error.message}`);
process.exit(result.status ?? 1);
