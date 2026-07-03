import {
  CloudOauthApiFactory,
  Configuration as AuthConfiguration,
} from "@/api/generated/auth-client";
import {
  Configuration as CoreConfiguration,
  DatasetsApiFactory as CoreDatasetsApiFactory,
  ModelProvidersApiFactory,
} from "@/api/generated/core-client";
import {
  Configuration as ScanConfiguration,
  ScanApi,
} from "@/api/generated/scan-client";
import { BASE_URL, axiosInstance } from "@/components/request";

// Generated OpenAPI clients are instantiated with only the basePath and the
// shared axiosInstance. Auth headers (Authorization / X-User-Id / X-Tenant-ID)
// are injected globally by the request interceptor in @/components/request, so
// the clients themselves carry no credentials.
const basePath = BASE_URL || window.location.origin;

export const dataSourceDatasetsApi = CoreDatasetsApiFactory(
  new CoreConfiguration({ basePath }),
  basePath,
  axiosInstance,
);

export const dataSourceModelProvidersApi = ModelProvidersApiFactory(
  new CoreConfiguration({ basePath }),
  basePath,
  axiosInstance,
);

export const dataSourceCloudOauthApi = CloudOauthApiFactory(
  new AuthConfiguration({ basePath }),
  basePath,
  axiosInstance,
);

export const dataSourceScanApi = new ScanApi(
  new ScanConfiguration({ basePath }),
  basePath,
  axiosInstance,
);
