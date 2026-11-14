package common

func init() {
	registerAdditionalErrorAlias("query external task states failed", "Failed to query external task states", 500, 2003220)
	registerAdditionalErrorAlias("query local task states failed", "Failed to query local task states", 500, 2003221)
	registerAdditionalErrorAlias("count tasks failed", "Failed to count tasks", 500, 2003222)
	registerAdditionalErrorAlias("unsupported pdf translation operation", "Unsupported PDF translation operation", 400, 2003223)
	registerAdditionalErrorAlias("pdf translation service is not configured", "PDF translation service is not configured", 503, 2003224)
	registerAdditionalErrorAlias("本地 pdf 翻译运行环境不可用，请修复或重新安装桌面应用后重试", "Local PDF translation runtime is unavailable; repair or reinstall the desktop app", 503, 2003225)
	registerAdditionalErrorAlias("本地 pdf 翻译组件缺失，请修复或重新安装桌面应用后重试", "Local PDF translation component is missing; repair or reinstall the desktop app", 503, 2003226)
	registerAdditionalErrorAlias("pdf 翻译字体准备失败，请检查网络后重试", "PDF translation font preparation failed; check your network and retry", 502, 2003227)
	registerAdditionalErrorAlias("本地 pdf 翻译排版失败，请重试；若仍失败，请修复桌面应用的 python 运行环境", "Local PDF translation layout failed; retry or repair the desktop Python runtime", 500, 2003228)
	registerAdditionalErrorAlias("本地 pdf 翻译组件返回了无效结果", "Local PDF translation returned an invalid result", 502, 2003229)
	registerAdditionalErrorAlias("mineru api request cannot be replayed", "MinerU request cannot be replayed; retry the operation", 502, 2003230)
	registerAdditionalErrorAlias("pdf font preparation is only available in local mode", "PDF font preparation is only available in local mode", 403, 2003231)
}
