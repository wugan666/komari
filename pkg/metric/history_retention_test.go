package metric

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestPermanentHistorySurvivesCleanupAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history.db")
	policy := RollupPolicy{Tiers: []RollupTier{{Interval: time.Minute, Retention: time.Hour}, {Interval: time.Hour, Retention: 24 * time.Hour}, {Interval: 24 * time.Hour, Retention: 30 * 24 * time.Hour}}}
	open := func() *Store {
		s, err := Open(ctx, SQLite(path, WithRollupPolicy(policy)))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := open()
	at := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	for _, name := range []string{"permanent", "finite"} {
		days := -1
		if name == "finite" {
			days = 1
		}
		if err := s.CreateMetric(ctx, Definition{Name: name, Type: TypeGauge, RetentionDays: days}); err != nil {
			t.Fatal(err)
		}
		if err := s.WriteBatch(ctx, []Point{{MetricName: name, EntityID: "offline", Timestamp: at, Value: 42}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Compact(ctx, at.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CleanupExpired(ctx, at.Add(400*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = open()
	defer s.Close()
	first, last, err := s.HistoryRange(ctx, "permanent", "offline")
	if err != nil || first == nil || last == nil || !first.Equal(at) || !last.Equal(at) {
		t.Fatalf("lost permanent history: %v %v %v", first, last, err)
	}
	first, last, err = s.HistoryRange(ctx, "finite", "offline")
	if err != nil || first != nil || last != nil {
		t.Fatalf("finite retention failed: %v %v %v", first, last, err)
	}
	points, err := s.AggregateRollup(ctx, AggregateQuery{Query: Query{MetricName: "permanent", EntityID: "offline", Start: at.Add(-24 * time.Hour), End: at.Add(24 * time.Hour)}, Aggregation: AggAvg, Interval: 24 * time.Hour}, 24*time.Hour)
	if err != nil || len(points) != 1 || points[0].Value != 42 {
		t.Fatalf("old history not queryable: %#v %v", points, err)
	}
	if _, err := s.SetMetricRetention(ctx, "permanent", 0); err != nil {
		t.Fatal(err)
	}
	first, _, err = s.HistoryRange(ctx, "permanent", "offline")
	if err != nil || first != nil {
		t.Fatalf("explicit disable failed: %v %v", first, err)
	}
}
