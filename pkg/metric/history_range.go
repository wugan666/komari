package metric

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// HistoryRange uses the original observation times retained in rollups, not
// bucket boundaries. It remains available after exact samples have expired.
func (s *Store) HistoryRange(ctx context.Context, metricName, entityID string) (*time.Time, *time.Time, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, nil, err
	}
	var first, last sql.NullInt64
	query := fmt.Sprintf(`SELECT MIN(r.first_ts_milli), MAX(r.last_ts_milli) FROM %s r JOIN %s s ON s.id = r.series_id WHERE s.metric_name = %s AND s.entity_id = %s`, s.tables.rollups, s.tables.series, s.dialect.placeholder(1), s.dialect.placeholder(2))
	if err := s.reader().QueryRowContext(ctx, query, metricName, entityID).Scan(&first, &last); err != nil {
		return nil, nil, err
	}
	if !first.Valid || !last.Valid {
		return nil, nil, nil
	}
	start, end := time.UnixMilli(first.Int64).UTC(), time.UnixMilli(last.Int64).UTC()
	return &start, &end, nil
}
