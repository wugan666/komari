// Package returnroute collects fixed-purpose IPv4 return-route observations.
package returnroute

import "time"

const (
	MaxHops      = 30
	HistoryLimit = 20
	JobTimeout   = 2 * time.Minute
	Cooldown     = 2 * time.Minute
)

type Target struct {
	Carrier string `json:"carrier"`
	IP      string `json:"ip"`
}

type City struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Targets []Target `json:"targets"`
}

var Cities = []City{
	{ID: "beijing", Name: "北京", Targets: []Target{{"CT", "219.141.140.10"}, {"CU", "202.106.195.68"}, {"CM", "221.179.155.161"}}},
	{ID: "shanghai", Name: "上海", Targets: []Target{{"CT", "202.96.209.133"}, {"CU", "210.22.97.1"}, {"CM", "211.136.112.200"}}},
	{ID: "guangzhou", Name: "广州", Targets: []Target{{"CT", "58.60.188.222"}, {"CU", "210.21.196.6"}, {"CM", "120.196.165.24"}}},
}

func FindCity(id string) (City, bool) {
	for _, city := range Cities {
		if city.ID == id {
			return city, true
		}
	}
	return City{}, false
}

type Hop struct {
	TTL int      `json:"ttl"`
	IP  string   `json:"ip,omitempty"`
	RTT *float64 `json:"rtt_ms,omitempty"`
	ASN string   `json:"asn,omitempty"`
}

type Classification struct {
	Code       string `json:"code"`
	Label      string `json:"label"`
	Grade      string `json:"grade,omitempty"`
	Confidence string `json:"confidence"`
	Evidence   string `json:"evidence"`
}

type Route struct {
	Carrier        string         `json:"carrier"`
	Target         string         `json:"target"`
	Reached        bool           `json:"reached"`
	Hops           []Hop          `json:"hops,omitempty"`
	Error          string         `json:"error,omitempty"`
	Classification Classification `json:"classification"`
	ChangedFrom    string         `json:"changed_from,omitempty"`
}

type Report struct {
	Schema int     `json:"schema"`
	City   string  `json:"city"`
	Routes []Route `json:"routes"`
}

type Snapshot struct {
	ID         string    `json:"id"`
	City       string    `json:"city"`
	MeasuredAt time.Time `json:"measured_at"`
	Routes     []Route   `json:"routes"`
}
