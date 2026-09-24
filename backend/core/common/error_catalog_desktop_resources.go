package common

import "net/http"

// Catalog desktop resource failures without exposing low-level diagnostics as UI messages.
func init() {
	registerAdditionalErrorAlias("pdf font download is only available in local mode", "PDF font download is only available in local mode", http.StatusForbidden, 2003117)
	registerAdditionalErrorAlias("pdf 中文字体准备失败，请检查网络后重试：", "PDF font preparation failed; check your network and retry", http.StatusBadGateway, 2003118)
	registerAdditionalErrorAlias("dependency installation is only supported in desktop/local mode", "Dependency installation is only supported in desktop/local mode", http.StatusForbidden, 2003119)
	registerAdditionalErrorAlias("unknown python dependency", "Unknown Python dependency", http.StatusBadRequest, 2003120)
	registerAdditionalErrorAlias("dependency installation is already running", "Dependency installation is already running", http.StatusConflict, 2003121)
	registerAdditionalErrorAlias("请先安装本地知识库组件，并重启本地服务。", "Install the local knowledge component and restart local services first", http.StatusServiceUnavailable, 2003122)
	registerAdditionalErrorAlias("asset archive exceeds expanded limit", "Resource download or verification failed", http.StatusBadGateway, 2003123)
	registerAdditionalErrorAlias("asset hash mismatch", "Resource download or verification failed", http.StatusBadGateway, 2003123)
	registerAdditionalErrorAlias("asset archive is incomplete", "Resource download or verification failed", http.StatusBadGateway, 2003123)
	registerAdditionalErrorAlias("asset download url must be https without embedded credentials or a fragment", "Resource download or verification failed", http.StatusBadGateway, 2003123)
	registerAdditionalErrorAlias("asset mirrors must reference the catalog filename", "Resource download or verification failed", http.StatusBadGateway, 2003123)
	registerAdditionalErrorAlias("primary asset source did not deliver data within the first-byte timeout", "Resource download or verification failed", http.StatusBadGateway, 2003123)
	registerAdditionalErrorAlias("primary asset source transfer speed is below the minimum", "Resource download or verification failed", http.StatusBadGateway, 2003123)
	registerAdditionalErrorAlias("too many asset redirects", "Resource download or verification failed", http.StatusBadGateway, 2003123)
	registerAdditionalErrorAlias("asset size or sha-256 does not match the build catalog", "Resource download or verification failed", http.StatusBadGateway, 2003123)
	registerAdditionalErrorAlias("asset download failed", "Resource download or verification failed", http.StatusBadGateway, 2003123)
	registerAdditionalErrorAlias("python dependency catalog does not match this platform", "Python dependency installation failed", http.StatusBadGateway, 2003124)
	registerAdditionalErrorAlias("dependency bundle is incompatible with this app version/platform", "Python dependency installation failed", http.StatusBadGateway, 2003124)
	registerAdditionalErrorAlias("dependency bundle has no site-packages", "Python dependency installation failed", http.StatusBadGateway, 2003124)
	registerAdditionalErrorAlias("unexpected dependency archive entry", "Python dependency installation failed", http.StatusBadGateway, 2003124)
	registerAdditionalErrorAlias("dependency archive exceeds expected expanded size", "Python dependency installation failed", http.StatusBadGateway, 2003124)
	registerAdditionalErrorAlias("dependency import verification failed", "Python dependency installation failed", http.StatusBadGateway, 2003124)
	registerAdditionalErrorPattern("unexpected ZIP entry %q", "Resource download or verification failed", http.StatusBadGateway, 2003123)
	registerAdditionalErrorPattern("asset download returned HTTP %d", "Resource download or verification failed", http.StatusBadGateway, 2003123)
}
