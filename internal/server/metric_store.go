package server

import (
	"context"
	"fmt"
	"github.com/komari-monitor/komari/database/clients"
	"time"

	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/internal/metricstore"
	logger "github.com/komari-monitor/komari/utils/log"
)

const (
	metricStoreReconnectAttempts = 3
	metricStoreReconnectInterval = 5 * time.Second
)

// ConnectMetricStore performs one connection attempt and registers cleanup
// only after a store has actually opened.
func (a *App) ConnectMetricStore() error {
	if err := metricstore.InitializeStore(); err != nil {
		return fmt.Errorf("failed to initialize metric store: %s", redactMetricStoreError(err))
	}
	if err := backfillLastSeen(); err != nil {
		logger.Errorf("presence", "Could not recover last-seen times from retained metrics: %v", err)
	}
	if !a.metricStoreCleanupAdded {
		a.addCleanup("metric-store", metricstore.CloseStoreContext)
		a.metricStoreCleanupAdded = true
	}
	return nil
}

func redactMetricStoreError(err error) string {
	if err == nil {
		return ""
	}
	dsn := ""
	if cfg, cfgErr := config.GetManyAs[metricstore.MetricStoreConfig](); cfgErr == nil {
		dsn = cfg.DSN
	}
	return metricstore.RedactConnectionError(err.Error(), dsn)
}

// ConnectMetricStoreWithRetry retries the monitoring database connection.
func (a *App) ConnectMetricStoreWithRetry() error {
	attempt := 0
	err := retryMetricStoreConnection(metricStoreReconnectAttempts, metricStoreReconnectInterval, func() error {
		attempt++
		err := a.ConnectMetricStore()
		if err != nil {
			logger.Warn("server", "Metric store connection attempt failed", "attempt", attempt, "max_attempts", metricStoreReconnectAttempts, "error", err)
		}
		return err
	})
	if err == nil && attempt > 1 {
		logger.Infof("server", "Metric store connection recovered on attempt %d/%d", attempt, metricStoreReconnectAttempts)
	}
	return err
}

func retryMetricStoreConnection(attempts int, interval time.Duration, connect func() error) error {
	if attempts < 1 {
		return fmt.Errorf("metric store retry attempts must be positive")
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			time.Sleep(interval)
		}
		if err := connect(); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	return lastErr
}

// InitStores opens the metric store and starts its report batcher. Historical
// data migrations are started only through an explicit administrator action.
func (a *App) InitStores() error {
	if err := a.ConnectMetricStore(); err != nil {
		auditlog.EventLog("error", fmt.Sprintf("Failed to initialize metric store: %v", err))
		return err
	}
	metricstore.StartReportBatcher()
	a.addCleanup("metric-report-batcher", metricstore.StopReportBatcher)
	// A store-to-store migration holds the exclusive operation lease. Stop it
	// before flushing queued reports, which need the shared lease to write.
	a.addCleanup("metric-store-migration", metricstore.StopStoreMigrationForShutdown)
	return nil
}

// Recover only timestamps supported by retained observations; configuration
// updated_at is deliberately not used as evidence that an agent was online.
func backfillLastSeen() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	nodes, err := clients.GetAllClientBasicInfo()
	if err != nil {
		return err
	}
	store := metricstore.GetStore()
	for _, node := range nodes {
		if node.LastSeenAt != nil {
			continue
		}
		var latest *time.Time
		for _, name := range []string{metricstore.MetricCPU, metricstore.MetricPingLatency} {
			_, last, err := store.HistoryRange(ctx, name, node.UUID)
			if err != nil {
				return err
			}
			if last != nil && (latest == nil || last.After(*latest)) {
				latest = last
			}
		}
		if latest != nil {
			if err := clients.RecordLastSeen(node.UUID, *latest); err != nil {
				return err
			}
		}
	}
	return nil
}
