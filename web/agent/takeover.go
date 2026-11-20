package agent

import (
	"sync"
	"time"
)

// A reconnecting agent replaces its own stale connection once. Two agent
// processes that share a token replace each other every few seconds, which
// shows up as a node that keeps flapping. Counting those takeovers lets the
// admin UI name the cause.

const (
	takeoverWindow    = 2 * time.Minute
	takeoverThreshold = 4
)

type takeoverState struct {
	times  []time.Time
	lastIP string
}

type ConnectionConflict struct {
	Takeovers int       `json:"takeovers"`
	LastAt    time.Time `json:"last_at"`
	LastIP    string    `json:"last_ip"`
}

var (
	takeoverMu sync.Mutex
	takeovers  = make(map[string]*takeoverState)
)

// RecordTakeover notes that a new connection displaced a live one for uuid and
// reports whether this crossed the conflict threshold just now.
func RecordTakeover(uuid, ip string) bool {
	now := time.Now()
	takeoverMu.Lock()
	defer takeoverMu.Unlock()
	state := takeovers[uuid]
	if state == nil {
		state = &takeoverState{}
		takeovers[uuid] = state
	}
	state.times = recentTakeovers(state.times, now)
	state.times = append(state.times, now)
	state.lastIP = ip
	return len(state.times) == takeoverThreshold
}

func recentTakeovers(times []time.Time, now time.Time) []time.Time {
	first := 0
	for first < len(times) && now.Sub(times[first]) > takeoverWindow {
		first++
	}
	return times[first:]
}

// ConnectionConflicts lists nodes whose connection is currently being fought
// over by more than one agent process.
func ConnectionConflicts() map[string]ConnectionConflict {
	now := time.Now()
	takeoverMu.Lock()
	defer takeoverMu.Unlock()
	result := make(map[string]ConnectionConflict)
	for uuid, state := range takeovers {
		state.times = recentTakeovers(state.times, now)
		if len(state.times) == 0 {
			delete(takeovers, uuid)
			continue
		}
		if len(state.times) >= takeoverThreshold {
			result[uuid] = ConnectionConflict{
				Takeovers: len(state.times),
				LastAt:    state.times[len(state.times)-1].UTC(),
				LastIP:    state.lastIP,
			}
		}
	}
	return result
}
