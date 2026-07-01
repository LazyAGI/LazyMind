import "./index.scss";
import DataSourceManagementView from "./components/DataSourceManagementView";
import { useDataSourceManagement } from "./hooks/useDataSourceManagement";

export default function DataSourceManagement() {
  const vm = useDataSourceManagement();
  return <DataSourceManagementView vm={vm} />;
}
