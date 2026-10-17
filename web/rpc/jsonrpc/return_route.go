package jsonrpc

import (
	"context"
	"errors"
	"strings"

	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/internal/returnroute"
	"github.com/komari-monitor/komari/pkg/rpc"
)

func init() {
	regPublic("getReturnRouteSummary", getReturnRouteSummary, "Read cached three-carrier return-route summaries for visible nodes")
	regPublic("getReturnRouteHistory", getReturnRouteHistory, "Read a visible node's cached return-route hops and recent history")
	RegisterWithGroupAndMeta("probeReturnRoute", rpc.RoleAdmin, probeReturnRoute, &rpc.MethodMeta{
		Name: "admin:probeReturnRoute", Summary: "Collect a node's IPv4 return routes to three fixed carrier targets",
		Description: "Runs a bounded, fixed Python collector through the existing Agent. Requires Linux, Python 3, raw ICMP permission and enabled remote control. Does not install software or accept arbitrary commands or targets.",
		Params:      []rpc.ParamMeta{{Name: "uuid", Type: "string", Required: true}, {Name: "city", Type: "string", Description: "beijing, shanghai or guangzhou"}},
		Returns:     "Diagnostic job metadata; poll public:getReturnRouteHistory for completion",
	})
}

// Resolve visibility against current node records, including deletions. Never
// return an orphaned archive merely because its UUID is known to the caller.
func visibleRouteNodes(ctx context.Context, requested []string) ([]string, *rpc.JsonRpcError) {
	if len(requested) > 200 {
		return nil, rpc.MakeError(rpc.InvalidParams, "at most 200 nodes per request", nil)
	}
	all, err := clients.GetAllClientBasicInfo()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to read nodes", nil)
	}
	wanted := map[string]bool{}
	for _, id := range requested {
		wanted[id] = true
	}
	loggedIn := isLoginFromCtx(ctx)
	visible := []string{}
	for _, node := range all {
		if node.Hidden && !loggedIn {
			continue
		}
		if len(requested) == 0 || wanted[node.UUID] {
			visible = append(visible, node.UUID)
		}
	}
	if len(visible) > 200 {
		return nil, rpc.MakeError(rpc.InvalidParams, "specify at most 200 node UUIDs", nil)
	}
	return visible, nil
}

func getReturnRouteSummary(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUIDs []string `json:"uuids"`
	}
	if req.Params != nil {
		if err := req.BindParams(&params); err != nil {
			return nil, rpc.MakeError(rpc.InvalidParams, "Invalid node selection", nil)
		}
	}
	nodes, rpcErr := visibleRouteNodes(ctx, params.UUIDs)
	if rpcErr != nil {
		return nil, rpcErr
	}
	summaries, err := returnroute.Summaries(ctx, nodes)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to read return routes", nil)
	}
	return map[string]any{"nodes": summaries, "cities": returnroute.Cities}, nil
}

func getReturnRouteHistory(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID string `json:"uuid"`
		City string `json:"city"`
		ID   string `json:"id"`
	}
	if req.BindParams(&params) != nil || strings.TrimSpace(params.UUID) == "" || len(params.ID) > 36 {
		return nil, rpc.MakeError(rpc.InvalidParams, "uuid is required", nil)
	}
	if params.City == "" {
		params.City = "beijing"
	}
	visible, rpcErr := visibleRouteNodes(ctx, []string{params.UUID})
	if rpcErr != nil {
		return nil, rpcErr
	}
	if len(visible) != 1 {
		return nil, rpc.MakeError(rpc.PermissionDenied, "Node not available", nil)
	}
	detail, err := returnroute.Read(ctx, params.UUID, params.City, params.ID)
	if errors.Is(err, returnroute.ErrCity) {
		return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
	}
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to read return-route history", nil)
	}
	return detail, nil
}

func probeReturnRoute(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID string `json:"uuid"`
		City string `json:"city"`
	}
	if req.BindParams(&params) != nil || strings.TrimSpace(params.UUID) == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "uuid is required", nil)
	}
	if params.City == "" {
		params.City = "beijing"
	}
	job, err := returnroute.Start(ctx, strings.TrimSpace(params.UUID), params.City)
	switch {
	case err == nil:
		actor, ip := auditActor(ctx)
		auditlog.Log(ip, actor, "Return-route collection: "+job.ID+" city="+job.City, "info")
		return job, nil
	case errors.Is(err, returnroute.ErrBusy), errors.Is(err, returnroute.ErrCooldown):
		return nil, rpc.MakeError(rpc.Unavailable, err.Error(), nil)
	case errors.Is(err, returnroute.ErrCity), errors.Is(err, returnroute.ErrPlatform), errors.Is(err, returnroute.ErrOffline):
		return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
	default:
		return nil, rpc.MakeError(rpc.InternalError, "Failed to start return-route collection", nil)
	}
}
