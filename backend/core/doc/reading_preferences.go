package doc

import (
	"encoding/json"
	"io"
	"lazymind/core/common"
	"lazymind/core/modelconfig"
	"lazymind/core/pdfir"
	"lazymind/core/readingpolicy"
	"lazymind/core/store"
	"lazymind/core/systemdeps"
	"net/http"
	"strings"
)

type readingCapability struct {
	readingpolicy.Capability
	Available         bool   `json:"available"`
	EffectiveMode     string `json:"effective_mode"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

func ReadingPreferences(w http.ResponseWriter, r *http.Request) {
	user := strings.TrimSpace(store.UserID(r))
	if user == "" {
		common.ReplyErr(w, "missing X-User-Id", 400)
		return
	}
	p, err := readingpolicy.Load(r.Context(), store.DB(), user)
	if err != nil {
		common.ReplyErr(w, "读取 AI 阅读准备设置失败", 500)
		return
	}
	if r.Method == http.MethodPut {
		var input readingpolicy.Policy
		d := json.NewDecoder(io.LimitReader(r.Body, 16384))
		d.DisallowUnknownFields()
		if err = d.Decode(&input); err != nil {
			common.ReplyErr(w, "准备设置格式无效", 400)
			return
		}
		var extra any
		if d.Decode(&extra) != io.EOF {
			common.ReplyErr(w, "只能提交一份准备设置", 400)
			return
		}
		p, err = readingpolicy.Save(r.Context(), store.DB(), user, input)
		if err != nil {
			common.ReplyErr(w, err.Error(), 400)
			return
		}
	}
	cfg, parseErr := modelconfig.LoadOCRConfig(r.Context(), store.DB(), user)
	if parseErr == nil {
		_, parseErr = pdfir.FromConfig(cfg)
	}
	_, _, _, modelErr := loadFormulaExplanationModel(r.Context(), user)
	caps := []readingCapability{}
	for _, descriptor := range readingpolicy.Registry() {
		key := descriptor.ID
		c := readingCapability{Capability: descriptor, Available: true, EffectiveMode: p.Modes[key]}
		switch key {
		case "overview":
			c.Available = false
			c.UnavailableReason = "论文速览尚未提供可运行的生成服务"
		case readingpolicy.PDFParse:
			if parseErr != nil {
				c.Available = false
				c.UnavailableReason = "请配置可用的 MinerU 解析服务"
			}
		case readingpolicy.ChatPrepare:
			if !systemdeps.PythonComponentActive("rag") {
				c.Available = false
				c.UnavailableReason = "请安装本地知识库组件"
			}
		default:
			if modelErr != nil {
				c.Available = false
				c.UnavailableReason = "请配置可用的聊天模型"
			}
			if parseErr != nil {
				c.UnavailableReason = "新文档需先配置 MinerU 解析服务；已有结构仍可研读"
			}
		}
		if !c.Available {
			c.EffectiveMode = "unavailable"
		}
		caps = append(caps, c)
	}
	common.ReplyOK(w, struct {
		readingpolicy.Policy
		Capabilities []readingCapability `json:"capabilities"`
	}{p, caps})
}
