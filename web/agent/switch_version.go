package agent

import (
	"errors"
	"strings"

	v2 "github.com/komari-monitor/komari/protocol/v2"
)

var (
	ErrSwitchVersionInvalidParams = errors.New("agent uuid and version are required")
	ErrSwitchVersionOffline       = errors.New("agent is offline")
)

func SwitchAgentVersion(uuid, version string) error {
	uuid = strings.TrimSpace(uuid)
	version = strings.TrimSpace(version)
	if uuid == "" || version == "" {
		return ErrSwitchVersionInvalidParams
	}
	if !IsAgentOnline(uuid) {
		return ErrSwitchVersionOffline
	}
	// This fork only accepts v2 agent connections. Supporting this method
	// additionally requires an agent release with version switching enabled.
	if !DispatchV2Event(uuid, v2.MethodAgentSwitchVersion, v2.SwitchVersionParams{Version: version}) {
		return ErrSwitchVersionOffline
	}
	return nil
}
