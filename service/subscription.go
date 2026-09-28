package service

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"
	"github.com/alireza0/s-ui/logger"
	"gorm.io/gorm"
)

type SubscriptionService struct {
	fetch func(string) (string, error)
}

const hproxyLiveURL = "https://raw.githubusercontent.com/hproxy-com/free-proxy-list/main/live.json"
const proxyScrapeLiveURL = "https://cdn.jsdelivr.net/gh/proxyscrape/free-proxy-list@main/proxies/all/data.json"

const userProvidedSubscriptionName = "User Provided Live Candidates"
const seededClientNodesSubscriptionName = "Local v2rayN Seed Nodes"

func EnsureHProxySubscription() error {
	return ensureCandidateSubscription("HProxy Live Candidates", hproxyLiveURL, 30)
}

func EnsureProxyScrapeSubscription() error {
	return ensureCandidateSubscription("ProxyScrape Live Candidates", proxyScrapeLiveURL, 30)
}

func EnsureUserProvidedSubscription() error {
	return ensureRuntimeCandidateSubscription(userProvidedSubscriptionName, strings.TrimSpace(os.Getenv("SUI_USER_CANDIDATE_URL")), 30, "replace")
}

func EnsureSeededClientNodesSubscription() error {
	path := strings.TrimSpace(os.Getenv("SUI_SEED_NODES_FILE"))
	if path != "" {
		path = "file://" + path
	}
	return ensureRuntimeCandidateSubscription(seededClientNodesSubscriptionName, path, 1440, "replace")
}

func ensureRuntimeCandidateSubscription(name, sourceURL string, interval int, updateMode string) error {
	db := database.GetDB()
	var existing model.Subscription
	if strings.TrimSpace(sourceURL) == "" {
		return db.Model(&model.Subscription{}).Where("name = ?", name).Updates(map[string]interface{}{
			"enabled": false,
			"url":     "",
		}).Error
	}
	if err := db.Where("name = ?", name).First(&existing).Error; err == nil {
		if existing.UpdateMode != updateMode {
			if err := db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Where("subscription_id = ?", existing.Id).Delete(&model.Outbound{}).Error; err != nil {
					return err
				}
				return tx.Model(&existing).Updates(map[string]interface{}{
					"node_count":  0,
					"last_update": 0,
				}).Error
			}); err != nil {
				return err
			}
		}
		return db.Model(&existing).Updates(map[string]interface{}{
			"url":             sourceURL,
			"enabled":         true,
			"update_interval": interval,
			"update_mode":     updateMode,
		}).Error
	} else if err != gorm.ErrRecordNotFound {
		return err
	}
	return db.Create(&model.Subscription{
		Name:           name,
		Url:            sourceURL,
		Enabled:        true,
		UpdateInterval: interval,
		UpdateMode:     updateMode,
		CreatedAt:      time.Now().Unix(),
	}).Error
}

func ensureCandidateSubscription(name, url string, interval int) error {
	db := database.GetDB()
	var existing model.Subscription
	if err := db.Where("url = ?", url).First(&existing).Error; err == nil {
		return nil
	} else if err != gorm.ErrRecordNotFound {
		return err
	}
	return db.Create(&model.Subscription{
		Name:           name,
		Url:            url,
		Enabled:        true,
		UpdateInterval: interval,
		UpdateMode:     "replace",
		CreatedAt:      time.Now().Unix(),
	}).Error
}

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
	fetch := s.fetchUrl
	if s.fetch != nil {
		fetch = s.fetch
	}
	content, err := fetch(subscription.Url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch subscription: %v", err)
	}

	// Parse subscription
	result, err := parseSubscriptionContent(content, subscription.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to parse subscription: %v", err)
	}
	if len(result.Outbounds) == 0 {
		return nil, fmt.Errorf("subscription produced no usable nodes; keeping the last known-good inventory")
	}

	db := database.GetDB()
	var existingCount int64
	if err := db.Model(&model.Outbound{}).Where("subscription_id = ?", id).Count(&existingCount).Error; err != nil {
		return nil, err
	}
	if subscription.UpdateMode == "replace" && existingCount > 0 {
		minimumAccepted := int(math.Max(1, math.Ceil(float64(existingCount)*0.2)))
		if len(result.Outbounds) < minimumAccepted {
			return nil, fmt.Errorf("subscription produced only %d nodes, below the safe replacement floor of %d; keeping the last known-good inventory", len(result.Outbounds), minimumAccepted)
		}
	}

	importResult := &RefreshResult{
		Success: 0,
		Failed:  len(result.Errors),
		Errors:  result.Errors,
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		if subscription.UpdateMode == "replace" {
			if err := tx.Where("subscription_id = ?", id).Delete(&model.Outbound{}).Error; err != nil {
				return err
			}
		}

		for _, outMap := range result.Outbounds {
			outbound := &model.Outbound{SubscriptionId: &id}

			outbound.Type, _ = outMap["type"].(string)
			outbound.Tag, _ = outMap["tag"].(string)
			outbound.LandingIP, _ = outMap["landing_ip"].(string)
			outbound.Country, _ = outMap["country"].(string)
			outbound.Region, _ = outMap["region"].(string)
			outbound.City, _ = outMap["city"].(string)
			delete(outMap, "type")
			delete(outMap, "tag")

			options, err := json.Marshal(outMap)
			if err != nil {
				importResult.Failed++
				importResult.Errors = append(importResult.Errors, fmt.Sprintf("Failed to serialize options: %v", err))
				continue
			}
			outbound.Options = options

			if subscription.UpdateMode == "incremental" {
				var existing model.Outbound
				if tx.Where("tag = ?", outbound.Tag).First(&existing).Error == nil {
					continue
				}
			}

			if err := tx.Create(outbound).Error; err != nil {
				return fmt.Errorf("failed to create outbound %q: %w", outbound.Tag, err)
			}
			importResult.Success++
		}
		return nil
	})
	if err != nil {
		return nil, err
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
	if strings.HasPrefix(url, "file://") {
		path := strings.TrimPrefix(url, "file://")
		body, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(body), nil
	}
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
func (s *SubscriptionService) StartAutoUpdate(afterRefresh func() error) {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for range ticker.C {
			s.checkAndUpdate(afterRefresh)
		}
	}()
}

func (s *SubscriptionService) checkAndUpdate(afterRefresh func() error) {
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
			result, err := s.Refresh(sub.Id)
			if err != nil {
				logger.Error("Failed to auto-update subscription", sub.Name, ":", err)
				continue
			}
			if result.Success > 0 && afterRefresh != nil {
				if err := afterRefresh(); err != nil {
					logger.Error("Failed to reload core after subscription update", sub.Name, ":", err)
				}
			}
		}
	}
}

type RefreshResult struct {
	Success int      `json:"success"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors"`
}
