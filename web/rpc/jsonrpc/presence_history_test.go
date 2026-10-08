package jsonrpc

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/rpc"
	"testing"
	"time"
)

func TestOfflineStatusUsesPersistedContactAndHidesPrivateNodes(t *testing.T) {
	db := dbcore.GetDBInstance()
	at := time.Now().UTC().Add(-120 * 24 * time.Hour).Truncate(time.Millisecond)
	visible, hidden := uuid.NewString(), uuid.NewString()
	for _, id := range []string{visible, hidden} {
		node := models.Client{UUID: id, Token: id, LastSeenAt: &at, Hidden: id == hidden}
		if err := db.Create(&node).Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Delete(&models.Client{}, "uuid = ?", id) })
	}
	ctx := rpc.NewContextWithMeta(context.Background(), &rpc.ContextMeta{})
	result, err := getNodesLatestStatus(ctx, &rpc.JsonRpcRequest{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	var states map[string]struct {
		Online bool      `json:"online"`
		Time   time.Time `json:"time"`
	}
	if e := json.Unmarshal(raw, &states); e != nil {
		t.Fatal(e)
	}
	state, ok := states[visible]
	if !ok || state.Online || !state.Time.Equal(at) {
		t.Fatalf("offline persisted state missing: %#v", state)
	}
	if _, ok := states[hidden]; ok {
		t.Fatal("hidden contact timestamp exposed")
	}
	_, rpcErr := publicGetPingHistoryRange(ctx, &rpc.JsonRpcRequest{Params: map[string]any{"entity_id": hidden}})
	if rpcErr == nil || rpcErr.Code != rpc.PermissionDenied {
		t.Fatalf("hidden history range allowed: %v", rpcErr)
	}
}
