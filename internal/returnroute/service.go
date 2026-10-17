package returnroute

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	v2 "github.com/komari-monitor/komari/protocol/v2"
	"github.com/komari-monitor/komari/web/agent"
	"gorm.io/gorm"
)

//go:embed collector.py
var collector string

var admission sync.Mutex

var (
	ErrCity     = errors.New("unsupported city")
	ErrOffline  = errors.New("node is offline")
	ErrPlatform = errors.New("return-route collection requires a Linux node")
	ErrBusy     = errors.New("a route collection is already running or the global limit of four has been reached")
	ErrCooldown = errors.New("please wait two minutes between collections for the same city")
	ErrReport   = errors.New("invalid return-route report")
)

func command(city string) string {
	return "command -v python3 >/dev/null 2>&1 || { printf 'KOMARI_RETURN_ROUTE_ERROR:python_missing\\n'; exit 1; }\nexec python3 -I -c 'import base64;exec(compile(base64.b64decode(\"" + base64.StdEncoding.EncodeToString([]byte(collector)) + "\"),\"<komari-return-route>\",\"exec\"))' " + city
}

func pendingState(record *models.ReturnRouteRecord) {
	if record.State == "pending" && time.Since(record.CreatedAt) >= JobTimeout {
		record.State, record.Error = "failed", "timed_out"
	}
}

func admit(tx *gorm.DB, node, city string, remote bool) error {
	now := time.Now().UTC()
	if err := tx.Model(&models.ReturnRouteRecord{}).Where("state = ? AND created_at < ?", "pending", now.Add(-JobTimeout)).Updates(map[string]any{"state": "failed", "error": "timed_out", "finished_at": now}).Error; err != nil {
		return err
	}
	var count int64
	if err := tx.Model(&models.ReturnRouteRecord{}).Where("client = ? AND city = ? AND created_at > ?", node, city, now.Add(-Cooldown)).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return ErrCooldown
	}
	if err := tx.Model(&models.ReturnRouteRecord{}).Where("client = ? AND state = ?", node, "pending").Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return ErrBusy
	}
	if remote {
		if err := tx.Model(&models.ReturnRouteRecord{}).Where("state = ?", "pending").Count(&count).Error; err != nil {
			return err
		}
		if count >= 4 {
			return ErrBusy
		}
	}
	return nil
}

// Start dispatches only the embedded collector and an allowlisted city. No
// caller-supplied command, destination, path or traceroute options are accepted.
func Start(ctx context.Context, node, city string) (*models.ReturnRouteRecord, error) {
	if _, ok := FindCity(city); !ok {
		return nil, ErrCity
	}
	client, err := clients.GetClientByUUID(node)
	if err != nil {
		return nil, err
	}
	if strings.Contains(strings.ToLower(client.OS), "windows") || strings.Contains(strings.ToLower(client.OS), "darwin") || strings.Contains(strings.ToLower(client.OS), "freebsd") {
		return nil, ErrPlatform
	}
	if !agent.IsAgentOnline(node) {
		return nil, ErrOffline
	}
	id := "rr-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	now := time.Now().UTC()
	record := &models.ReturnRouteRecord{ID: id, Client: node, City: city, State: "pending", CreatedAt: now}
	cmd := command(city)
	admission.Lock()
	err = dbcore.GetDBInstance().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := admit(tx, node, city, true); err != nil {
			return err
		}
		if err := tx.Create(record).Error; err != nil {
			return err
		}
		if err := tx.Create(&models.Task{TaskId: id, Clients: models.StringArray{node}, Command: cmd}).Error; err != nil {
			return err
		}
		return tx.Create(&models.TaskResult{TaskId: id, Client: node, CreatedAt: now}).Error
	})
	admission.Unlock()
	if err != nil {
		return nil, err
	}
	if !agent.DispatchV2Event(node, v2.MethodAgentExec, v2.ExecParams{TaskID: id, Command: cmd}) {
		_ = finish(node, id, nil, "offline")
		return nil, ErrOffline
	}
	return record, nil
}

func normalize(report Report) (*Report, string, error) {
	city, ok := FindCity(report.City)
	if !ok || report.Schema != 1 || len(report.Routes) != 3 {
		return nil, "", ErrReport
	}
	ordered := make([]Route, 0, 3)
	responses := 0
	for _, target := range city.Targets {
		var found *Route
		for i := range report.Routes {
			if report.Routes[i].Carrier == target.Carrier {
				if found != nil {
					return nil, "", ErrReport
				}
				found = &report.Routes[i]
			}
		}
		if found == nil || found.Target != target.IP || len(found.Hops) > MaxHops {
			return nil, "", ErrReport
		}
		route := Route{Carrier: target.Carrier, Target: target.IP, Hops: make([]Hop, 0, len(found.Hops))}
		switch found.Error {
		case "", "permission_denied", "network_unavailable", "unsupported_platform", "destination_unreachable":
			route.Error = found.Error
		default:
			return nil, "", ErrReport
		}
		last, answered := 0, 0
		for _, hop := range found.Hops {
			if hop.TTL <= last || hop.TTL > MaxHops {
				return nil, "", ErrReport
			}
			last = hop.TTL
			hop.ASN = ""
			if hop.IP != "" {
				ip, err := netip.ParseAddr(hop.IP)
				if err != nil || !ip.Is4() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLoopback() {
					return nil, "", ErrReport
				}
				hop.IP = ip.String()
				if hop.RTT == nil || math.IsNaN(*hop.RTT) || math.IsInf(*hop.RTT, 0) || *hop.RTT < 0 || *hop.RTT > 10000 {
					return nil, "", ErrReport
				}
				hop.ASN = hopASN(hop.IP)
				answered++
				responses++
			} else {
				hop.RTT = nil
			}
			route.Hops = append(route.Hops, hop)
		}
		if len(route.Hops) > 0 {
			route.Reached = found.Reached && route.Hops[len(route.Hops)-1].IP == target.IP && route.Error == ""
		}
		if answered == 0 && route.Error == "" {
			route.Error = "no_reply"
		}
		route.Classification = classify(target.Carrier, route.Hops)
		ordered = append(ordered, route)
	}
	report.Routes = ordered
	if responses == 0 {
		code := ordered[0].Error
		for _, route := range ordered {
			if route.Error != code {
				code = "no_reply"
				break
			}
		}
		return &report, code, nil
	}
	return &report, "", nil
}

// Complete accepts an execution result only for the authenticated node's job.
// Raw stderr is never reflected into the public diagnostic history.
func Complete(node, id, output string, exitCode int) error {
	if !strings.HasPrefix(id, "rr-") {
		return nil
	}
	if len(output) > 65536 {
		return finish(node, id, nil, "invalid_report")
	}
	output = strings.TrimSpace(output)
	if strings.Contains(output, "Remote control is disabled.") {
		return finish(node, id, nil, "remote_control_disabled")
	}
	if strings.HasPrefix(output, "KOMARI_RETURN_ROUTE_ERROR:python_missing") {
		return finish(node, id, nil, "python_missing")
	}
	if exitCode != 0 {
		return finish(node, id, nil, "collection_failed")
	}
	const marker = "KOMARI_RETURN_ROUTE_V1\n"
	if !strings.HasPrefix(output, marker) {
		return finish(node, id, nil, "invalid_report")
	}
	var report Report
	if json.Unmarshal([]byte(strings.TrimPrefix(output, marker)), &report) != nil {
		return finish(node, id, nil, "invalid_report")
	}
	normalized, failure, err := normalize(report)
	if err != nil {
		return finish(node, id, nil, "invalid_report")
	}
	return finish(node, id, normalized, failure)
}

// Receive supports a collector run locally by a node operator. As with metric
// reports, identity always comes from the authenticated Agent token, never JSON.
func Receive(ctx context.Context, node string, report Report) (*models.ReturnRouteRecord, error) {
	normalized, failure, err := normalize(report)
	if err != nil {
		return nil, err
	}
	if _, err := clients.GetClientByUUID(node); err != nil {
		return nil, err
	}
	record := &models.ReturnRouteRecord{ID: uuid.NewString(), Client: node, City: report.City, State: "pending", CreatedAt: time.Now().UTC()}
	admission.Lock()
	err = dbcore.GetDBInstance().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := admit(tx, node, report.City, false); err != nil {
			return err
		}
		return tx.Create(record).Error
	})
	admission.Unlock()
	if err != nil {
		return nil, err
	}
	if err := finish(node, record.ID, normalized, failure); err != nil {
		return nil, err
	}
	if err := dbcore.GetDBInstance().WithContext(ctx).First(record, "id = ? AND client = ?", record.ID, node).Error; err != nil {
		return nil, err
	}
	return record, nil
}

func finish(node, id string, report *Report, failure string) error {
	admission.Lock()
	defer admission.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return dbcore.GetDBInstance().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record models.ReturnRouteRecord
		if err := tx.First(&record, "id = ? AND client = ?", id, node).Error; err != nil {
			return err
		}
		if record.State != "pending" {
			return nil
		}
		now := time.Now().UTC()
		if now.Sub(record.CreatedAt) > JobTimeout {
			failure = "timed_out"
		}
		if report != nil && report.City != record.City {
			failure = "invalid_report"
		}
		values := map[string]any{"finished_at": now, "state": "failed", "error": failure}
		if failure == "" && report != nil {
			snapshot := Snapshot{ID: id, City: record.City, MeasuredAt: now, Routes: report.Routes}
			var previous models.ReturnRouteRecord
			err := tx.Where("client = ? AND city = ? AND state = ?", node, record.City, "complete").Order("created_at DESC, id DESC").First(&previous).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			var old Snapshot
			if err == nil && json.Unmarshal([]byte(previous.Summary), &old) == nil {
				for i := range snapshot.Routes {
					for _, prior := range old.Routes {
						current := &snapshot.Routes[i]
						if current.Carrier == prior.Carrier && current.Classification.Confidence != "inconclusive" && prior.Classification.Confidence != "inconclusive" && current.Classification.Code != prior.Classification.Code {
							current.ChangedFrom = prior.Classification.Label
						}
					}
				}
			}
			payload, err := json.Marshal(snapshot)
			if err != nil {
				return err
			}
			for i := range snapshot.Routes {
				snapshot.Routes[i].Hops = nil
			}
			summary, err := json.Marshal(snapshot)
			if err != nil {
				return err
			}
			values["state"], values["summary"], values["payload"] = "complete", string(summary), string(payload)
		}
		if err := tx.Model(&record).Updates(values).Error; err != nil {
			return err
		}
		var stale []string
		if err := tx.Model(&models.ReturnRouteRecord{}).Where("client = ? AND city = ? AND state = ?", node, record.City, "complete").Order("created_at DESC, id DESC").Offset(HistoryLimit).Pluck("id", &stale).Error; err != nil {
			return err
		}
		// Keep the last failure separately so failed attempts cannot evict the
		// last useful route or consume the twenty successful history slots.
		var oldFailures []string
		if err := tx.Model(&models.ReturnRouteRecord{}).Where("client = ? AND city = ? AND state = ?", node, record.City, "failed").Order("created_at DESC, id DESC").Offset(1).Pluck("id", &oldFailures).Error; err != nil {
			return err
		}
		stale = append(stale, oldFailures...)
		if len(stale) > 0 {
			if err := tx.Where("id IN ?", stale).Delete(&models.ReturnRouteRecord{}).Error; err != nil {
				return err
			}
			if err := tx.Where("task_id IN ?", stale).Delete(&models.TaskResult{}).Error; err != nil {
				return err
			}
			if err := tx.Where("task_id IN ?", stale).Delete(&models.Task{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

type Attempt struct {
	ID         string     `json:"id"`
	State      string     `json:"state"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Error      string     `json:"error,omitempty"`
	Routes     []Route    `json:"routes,omitempty"`
}

type Detail struct {
	City            string    `json:"city"`
	Cities          []City    `json:"cities"`
	LatestAttempt   *Attempt  `json:"latest_attempt"`
	Snapshot        *Snapshot `json:"snapshot"`
	History         []Attempt `json:"history"`
	HistoryLimit    int       `json:"history_limit"`
	CooldownSeconds int       `json:"cooldown_seconds"`
}

func Read(ctx context.Context, node, city, selectedID string) (*Detail, error) {
	if _, ok := FindCity(city); !ok {
		return nil, ErrCity
	}
	db := dbcore.GetDBInstance().WithContext(ctx)
	var records []models.ReturnRouteRecord
	if err := db.Select("id, city, state, created_at, finished_at, error, summary").Where("client = ? AND city = ? AND state = ?", node, city, "complete").Order("created_at DESC, id DESC").Limit(HistoryLimit).Find(&records).Error; err != nil {
		return nil, err
	}
	detail := &Detail{City: city, Cities: Cities, History: []Attempt{}, HistoryLimit: HistoryLimit, CooldownSeconds: int(Cooldown.Seconds())}
	var latest models.ReturnRouteRecord
	err := db.Select("id, state, created_at, finished_at, error").Where("client = ? AND city = ?", node, city).Order("created_at DESC, id DESC").First(&latest).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err == nil {
		pendingState(&latest)
		detail.LatestAttempt = &Attempt{ID: latest.ID, State: latest.State, StartedAt: latest.CreatedAt, FinishedAt: latest.FinishedAt, Error: latest.Error}
	}
	selected := ""
	for _, record := range records {
		pendingState(&record)
		attempt := Attempt{ID: record.ID, State: record.State, StartedAt: record.CreatedAt, FinishedAt: record.FinishedAt, Error: record.Error}
		var summary Snapshot
		if record.State == "complete" && json.Unmarshal([]byte(record.Summary), &summary) == nil {
			attempt.Routes = summary.Routes
		}
		detail.History = append(detail.History, attempt)
		if record.State == "complete" && (selectedID == record.ID || selectedID == "" && selected == "") {
			selected = record.ID
		}
	}
	if selected != "" {
		var record models.ReturnRouteRecord
		if err := db.Select("payload").First(&record, "id = ? AND client = ?", selected, node).Error; err != nil {
			return nil, err
		}
		var snapshot Snapshot
		if err := json.Unmarshal([]byte(record.Payload), &snapshot); err != nil {
			return nil, err
		}
		detail.Snapshot = &snapshot
	}
	return detail, nil
}

func Summaries(ctx context.Context, nodes []string) (map[string]Snapshot, error) {
	out := make(map[string]Snapshot)
	if len(nodes) == 0 {
		return out, nil
	}
	var records []models.ReturnRouteRecord
	err := dbcore.GetDBInstance().WithContext(ctx).Raw(`SELECT client, summary FROM (
		SELECT client, summary, ROW_NUMBER() OVER (PARTITION BY client ORDER BY created_at DESC, id DESC) AS rank
		FROM return_route_records WHERE client IN ? AND state = 'complete'
	) WHERE rank = 1`, nodes).Scan(&records).Error
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		var snapshot Snapshot
		if json.Unmarshal([]byte(record.Summary), &snapshot) == nil {
			out[record.Client] = snapshot
		}
	}
	return out, nil
}
