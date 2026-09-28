package service

import (
	"testing"

	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestRefreshRejectsEmptyParsedPayloadWithoutDeletingInventory(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&model.Subscription{}, &model.Outbound{}); err != nil {
		t.Fatal(err)
	}
	database.SetDB(db)

	subscription := model.Subscription{Name: "known-good", Url: "http://127.0.0.1:1", UpdateMode: "replace"}
	if err = db.Create(&subscription).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.Outbound{Tag: "known-good-node", Type: "socks", SubscriptionId: &subscription.Id}).Error; err != nil {
		t.Fatal(err)
	}

	service := SubscriptionService{}
	service.fetch = func(string) (string, error) { return "not a subscription", nil }
	if _, err = service.Refresh(subscription.Id); err == nil {
		t.Fatal("expected an invalid payload to be rejected")
	}

	var count int64
	if err = db.Model(&model.Outbound{}).Where("subscription_id = ?", subscription.Id).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("replace refresh deleted known-good inventory: got %d rows", count)
	}
}
