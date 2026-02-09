package service

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"
	"github.com/alireza0/s-ui/logger"
	"github.com/alireza0/s-ui/util"
)

type SubscriptionService struct{}

// GetAll returns all subscriptions
func (s *SubscriptionService) GetAll() ([]model.Subscription, error) {
	db := database.GetDB()
	var subscriptions []model.Subscription
	err := db.Find(&subscriptions).Error
	return subscriptions, err
}

// GetById returns a subscription by ID
func (s *SubscriptionService) GetById(id uint) (*model.Subscription, error) {
	db := database.GetDB()
	var subscription model.Subscription
	err := db.First(&subscription, id).Error
	return &subscription, err
}

// Add creates a new subscription
func (s *SubscriptionService) Add(name, url, updateMode string, interval int) (*model.Subscription, error) {
	db := database.GetDB()
	
	subscription := &model.Subscription{
		Name:           name,
		Url:            url,
		Enabled:        true,
		UpdateInterval: interval,
		UpdateMode:     updateMode,
		CreatedAt:      time.Now().Unix(),
	}
	
	err := db.Create(subscription).Error
	if err != nil {
		return nil, err
	}
	
	return subscription, nil
}

// Update updates a subscription
func (s *SubscriptionService) Update(id uint, name, url, updateMode string, interval int, enabled bool) error {
	db := database.GetDB()
	
	return db.Model(&model.Subscription{}).Where("id = ?", id).Updates(map[string]interface{}{
		"name":            name,
		"url":             url,
		"update_mode":     updateMode,
		"update_interval": interval,
		"enabled":         enabled,
	}).Error
}

// Delete removes a subscription and its associated outbounds
func (s *SubscriptionService) Delete(id uint) error {
	db := database.GetDB()
	
	// Delete associated outbounds first
	err := db.Where("subscription_id = ?", id).Delete(&model.Outbound{}).Error
	if err != nil {
		return err
	}
	
	// Delete subscription
	return db.Delete(&model.Subscription{}, id).Error
}

// Refresh fetches and updates outbounds from subscription URL
func (s *SubscriptionService) Refresh(id uint) (*RefreshResult, error) {
	subscription, err := s.GetById(id)
	if err != nil {
		return nil, err
	}
	
	// Fetch subscription content
	content, err := s.fetchUrl(subscription.Url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch subscription: %v", err)
	}
	
	// Parse subscription
	result, err := util.ParseSubscription(content, subscription.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to parse subscription: %v", err)
	}
	
	db := database.GetDB()
	
	// Handle update mode
	if subscription.UpdateMode == "replace" {
		// Delete existing outbounds from this subscription
		err = db.Where("subscription_id = ?", id).Delete(&model.Outbound{}).Error
		if err != nil {
			return nil, err
		}
	}
	
	// Import new outbounds
	importResult := &RefreshResult{
		Success: 0,
		Failed:  len(result.Errors),
		Errors:  result.Errors,
	}
	
	for _, outMap := range result.Outbounds {
		outbound := &model.Outbound{
			SubscriptionId: &id,
		}
		
		// Set type and tag
		outbound.Type, _ = outMap["type"].(string)
		outbound.Tag, _ = outMap["tag"].(string)
		
		// Remove type and tag from options
		delete(outMap, "type")
		delete(outMap, "tag")
		
		// Serialize remaining options
		options, err := json.Marshal(outMap)
		if err != nil {
			importResult.Failed++
			importResult.Errors = append(importResult.Errors, fmt.Sprintf("Failed to serialize options: %v", err))
			continue
		}
		outbound.Options = options
		
		// Check for existing tag (for incremental mode)
		if subscription.UpdateMode == "incremental" {
			var existing model.Outbound
			if db.Where("tag = ?", outbound.Tag).First(&existing).Error == nil {
				// Tag exists, skip
				continue
			}
		}
		
		// Create outbound
		err = db.Create(outbound).Error
		if err != nil {
			importResult.Failed++
			importResult.Errors = append(importResult.Errors, fmt.Sprintf("Failed to create outbound: %v", err))
			continue
		}
		importResult.Success++
	}
	
	// Update subscription
	db.Model(&model.Subscription{}).Where("id = ?", id).Updates(map[string]interface{}{
		"last_update": time.Now().Unix(),
		"node_count":  importResult.Success,
	})
	
	return importResult, nil
}

// RefreshMultiple refreshes multiple subscriptions
func (s *SubscriptionService) RefreshMultiple(ids []uint) (map[uint]*RefreshResult, error) {
	results := make(map[uint]*RefreshResult)
	
	for _, id := range ids {
		result, err := s.Refresh(id)
		if err != nil {
			results[id] = &RefreshResult{
				Success: 0,
				Failed:  1,
				Errors:  []string{err.Error()},
			}
		} else {
			results[id] = result
		}
	}
	
	return results, nil
}

// fetchUrl fetches content from a URL
func (s *SubscriptionService) fetchUrl(url string) (string, error) {
	// Create a custom client directly to skip TLS verification
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   30 * time.Second,
	}
	
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP status: %d", resp.StatusCode)
	}
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	
	return string(body), nil
}

// StartAutoUpdate starts the auto-update goroutine
func (s *SubscriptionService) StartAutoUpdate() {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		
		for range ticker.C {
			s.checkAndUpdate()
		}
	}()
}

func (s *SubscriptionService) checkAndUpdate() {
	subscriptions, err := s.GetAll()
	if err != nil {
		logger.Error("Failed to get subscriptions for auto-update:", err)
		return
	}
	
	now := time.Now().Unix()
	
	for _, sub := range subscriptions {
		if !sub.Enabled || sub.UpdateInterval <= 0 {
			continue
		}
		
		// Check if it's time to update
		intervalSeconds := int64(sub.UpdateInterval * 60)
		if now-sub.LastUpdate >= intervalSeconds {
			logger.Info("Auto-updating subscription:", sub.Name)
			_, err := s.Refresh(sub.Id)
			if err != nil {
				logger.Error("Failed to auto-update subscription", sub.Name, ":", err)
			}
		}
	}
}

type RefreshResult struct {
	Success int      `json:"success"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors"`
}
