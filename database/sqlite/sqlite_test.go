package sqlite

import (
	"testing"
	"time"

	"gorm.io/gorm"
)

type testRow struct {
	ID   int `gorm:"primaryKey"`
	Name string
}

func TestSQLiteCRUDAndTransaction(t *testing.T) {
	db, err := gorm.Open((Config{DSN: "file:test-crud?mode=memory&cache=shared"}).Dialector(), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&testRow{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&testRow{Name: "before"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return tx.Create(&testRow{Name: "committed"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&testRow{Name: "rolled back"}).Error; err != nil {
			return err
		}
		return gorm.ErrInvalidTransaction
	}); err == nil {
		t.Fatal("expected rollback error")
	}
	var rows []testRow
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestSQLiteDSNPragmas(t *testing.T) {
	dsn := (Config{DSN: "file:test-pragmas?mode=memory&cache=shared", WAL: true, BusyTimeout: 3 * time.Second, ForeignKeys: true}).dsn()
	if dsn == "" || dsn == "file:test-pragmas?mode=memory&cache=shared" {
		t.Fatalf("expected pragma parameters in DSN: %s", dsn)
	}
}
