package models

import "time"

// ReturnRouteRecord keeps bounded diagnostic history independently of metric retention.
type ReturnRouteRecord struct {
	ID         string     `json:"id" gorm:"type:varchar(36);primaryKey"`
	Client     string     `json:"client" gorm:"type:varchar(36);index:idx_return_route_node_city,priority:1"`
	ClientInfo Client     `json:"-" gorm:"foreignKey:Client;references:UUID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
	City       string     `json:"city" gorm:"type:varchar(16);index:idx_return_route_node_city,priority:2"`
	State      string     `json:"state" gorm:"type:varchar(16);index"`
	Error      string     `json:"error,omitempty"`
	CreatedAt  time.Time  `json:"started_at" gorm:"index:idx_return_route_node_city,priority:3"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Summary    string     `json:"-" gorm:"type:text"`
	Payload    string     `json:"-" gorm:"type:text"`
}
