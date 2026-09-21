import type { TablePaginationConfig } from "antd";

export type TablePaginationProp = TablePaginationConfig | false | undefined;

export function getLocalizedTablePagination(
  pagination: TablePaginationProp,
  t: (key: "common.itemsPerPageSuffix" | "common.pageSize") => string,
): TablePaginationProp {
  if (!pagination) {
    return pagination;
  }

  return {
    ...pagination,
    locale: {
      ...pagination.locale,
      items_per_page: t("common.itemsPerPageSuffix"),
      page_size: t("common.pageSize"),
    },
  };
}
