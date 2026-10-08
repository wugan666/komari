package jsonrpc

import (
	"context"
	"strings"

	"github.com/komari-monitor/komari/internal/metricstore"
	"github.com/komari-monitor/komari/pkg/rpc"
)

func init() {
	regPublic("getPingHistoryRange", publicGetPingHistoryRange, "Get the available ping history range for one visible node")
}

func publicGetPingHistoryRange(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		EntityID string `json:"entity_id"`
	}
	if err := req.BindParams(&params); err != nil || strings.TrimSpace(params.EntityID) == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "entity_id is required", nil)
	}
	ctx, release, err := acquireHistoryQuery(ctx)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "history query canceled", nil)
	}
	defer release()
	visible, rpcErr := publicMetricEntityIDs(ctx, []string{params.EntityID})
	if rpcErr != nil {
		return nil, rpcErr
	}
	if len(visible) != 1 {
		return nil, rpc.MakeError(rpc.PermissionDenied, "Node not available", nil)
	}
	store := metricstore.GetStore()
	if store == nil {
		return nil, rpc.MakeError(rpc.InternalError, "metric store not initialized", nil)
	}
	first, last, err := store.HistoryRange(ctx, metricstore.MetricPingLatency, params.EntityID)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to read history range", nil)
	}
	def, err := store.GetMetric(ctx, metricstore.MetricPingLatency)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to read history policy", nil)
	}
	return map[string]any{"first_sample": first, "last_sample": last, "retention_days": def.RetentionDays}, nil
}
