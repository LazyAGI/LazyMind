package feishu

import (
	"context"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

func (c *FeishuConnector) search(ctx context.Context, req connector.SearchRequest) (connector.RawObjectPage, error) {
	if err := ctx.Err(); err != nil {
		return connector.RawObjectPage{}, err
	}
	if strings.TrimSpace(req.Keyword) == "" {
		return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeInvalidArgument, "keyword is required")
	}
	if req.TargetType != "" && !isSupportedTargetType(req.TargetType) {
		return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeInvalidTarget, "target_type is not supported")
	}
	return connector.RawObjectPage{}, connector.NewError(connector.ErrorCodeUnsupported, "feishu search API is unsupported for this target scope")
}
