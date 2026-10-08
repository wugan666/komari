package clients

import (
	"github.com/komari-monitor/komari/database/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"path/filepath"
	"testing"
	"time"
)

func TestLastSeenSurvivesReopenWithoutChangingConfigurationTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clients.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&models.Client{}); err != nil {
		t.Fatal(err)
	}
	original := time.Now().UTC().Add(-365 * 24 * time.Hour).Truncate(time.Millisecond)
	at := original.Add(2 * time.Hour)
	if err = db.Create(&models.Client{UUID: "offline", Token: "fixture", UpdatedAt: original}).Error; err != nil {
		t.Fatal(err)
	}
	if err = recordLastSeen(db, "offline", at); err != nil {
		t.Fatal(err)
	}
	if err = recordLastSeen(db, "offline", original); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.Close()
	db, err = gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ = db.DB()
	defer sqlDB.Close()
	var node models.Client
	if err = db.First(&node, "uuid = ?", "offline").Error; err != nil {
		t.Fatal(err)
	}
	if node.LastSeenAt == nil || !node.LastSeenAt.Equal(at) || !node.UpdatedAt.Equal(original) {
		t.Fatalf("presence lost or configuration changed: %#v", node)
	}
}
