package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alireza0/s-ui/logger"
)

// PhysicalServerEntry represents a validated physical server in a specific country
type PhysicalServerEntry struct {
	Name      string  `json:"name"`
	Country   string  `json:"country"` // ISO 3166-1 alpha-2, uppercase (e.g. "BE", "AR", "BR", "US", "NL")
	City      string  `json:"city"`
	Domain    string  `json:"domain"`
	EntryIP   string  `json:"entry_ip"`
	ExitIP    string  `json:"exit_ip"`
	PublicKey string  `json:"public_key"`
	Port      int     `json:"port"`
	Tier      int     `json:"tier"` // 0 = Free, 1/2 = Standard/Plus
	Load      int     `json:"load"`
	Score     float64 `json:"score"`
}

// MultiCountryCache manages dynamic caching and updates for physical server endpoints
type MultiCountryCache struct {
	mu           sync.RWMutex
	serversByLoc map[string][]*PhysicalServerEntry
	lastUpdated  time.Time
	activeSource string
}

var (
	globalCountryCache *MultiCountryCache
	countryCacheOnce   sync.Once
)

// GetCountryCache returns the singleton multi-country cache manager
func GetCountryCache() *MultiCountryCache {
	countryCacheOnce.Do(func() {
		globalCountryCache = &MultiCountryCache{
			serversByLoc: make(map[string][]*PhysicalServerEntry),
		}
		_ = globalCountryCache.ReloadFromDisk()
	})
	return globalCountryCache
}

// candidateCachePaths returns prioritized paths where cached_logicals.json may reside
func candidateCachePaths() []string {
	paths := []string{
		filepath.Join("/usr/local/s-ui", "scripts", "cached_logicals.json"),
		filepath.Join("scripts", "cached_logicals.json"),
	}
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), "scripts", "cached_logicals.json"))
	}
	paths = append(paths, filepath.Join("i:", "learn_code", "s-ui", "scripts", "cached_logicals.json"))
	return paths
}

// ReloadFromDisk parses the local JSON cache file and populates the in-memory registry
func (c *MultiCountryCache) ReloadFromDisk() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var data []byte
	var chosenPath string

	for _, p := range candidateCachePaths() {
		if content, err := os.ReadFile(p); err == nil && len(content) > 100 {
			data = content
			chosenPath = p
			break
		}
	}

	if len(data) == 0 {
		return fmt.Errorf("no valid cached_logicals.json found in candidate paths")
	}

	var rawServers []*ProtonLogicalServer
	if err := json.Unmarshal(data, &rawServers); err != nil {
		return fmt.Errorf("failed to unmarshal cached logicals: %w", err)
	}

	newMap := make(map[string][]*PhysicalServerEntry)
	totalLoaded := 0

	for _, ls := range rawServers {
		if ls.Status != 1 || len(ls.Servers) == 0 {
			continue
		}
		cCode := strings.ToUpper(strings.TrimSpace(ls.ExitCountry))
		if cCode == "" {
			cCode = strings.ToUpper(strings.TrimSpace(ls.EntryCountry))
		}
		if len(cCode) != 2 {
			continue
		}

		for _, ps := range ls.Servers {
			if ps.Status != 1 || ps.EntryIP == "" || ps.X25519PublicKey == "" {
				continue
			}

			entry := &PhysicalServerEntry{
				Name:      ls.Name,
				Country:   cCode,
				City:      ls.City,
				Domain:    ps.Domain,
				EntryIP:   ps.EntryIP,
				ExitIP:    ps.ExitIP,
				PublicKey: ps.X25519PublicKey,
				Port:      51820,
				Tier:      ls.Tier,
				Load:      ls.Load,
				Score:     ls.Score,
			}
			newMap[cCode] = append(newMap[cCode], entry)
			totalLoaded++
		}
	}

	// Sort each country's servers: Tier 0 first (Free), then lowest Load first
	for _, sList := range newMap {
		sort.Slice(sList, func(i, j int) bool {
			if sList[i].Tier != sList[j].Tier {
				return sList[i].Tier < sList[j].Tier
			}
			return sList[i].Load < sList[j].Load
		})
	}

	c.serversByLoc = newMap
	c.lastUpdated = time.Now()
	c.activeSource = chosenPath

	logger.Infof("MultiCountryCache: Loaded %d physical servers across %d countries from %s",
		totalLoaded, len(newMap), chosenPath)
	return nil
}

// GetCountryServers returns all physical servers for a given ISO country code (e.g. "BE", "AR")
func (c *MultiCountryCache) GetCountryServers(countryCode string) []*PhysicalServerEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	code := strings.ToUpper(strings.TrimSpace(countryCode))
	if list, ok := c.serversByLoc[code]; ok && len(list) > 0 {
		cp := make([]*PhysicalServerEntry, len(list))
		copy(cp, list)
		return cp
	}
	return nil
}

// GetCityServers returns physical servers matching a country code and city name/IATA code
func (c *MultiCountryCache) GetCityServers(countryCode string, cityQuery string) []*PhysicalServerEntry {
	allCountry := c.GetCountryServers(countryCode)
	if len(allCountry) == 0 {
		return nil
	}
	query := strings.ToLower(strings.TrimSpace(cityQuery))
	if query == "" {
		return allCountry
	}
	var matched []*PhysicalServerEntry
	for _, s := range allCountry {
		if strings.Contains(strings.ToLower(s.City), query) || strings.Contains(strings.ToLower(s.Domain), query) {
			matched = append(matched, s)
		}
	}
	if len(matched) > 0 {
		return matched
	}
	return allCountry // fallback to country servers if specific city has no dedicated box
}

// BuildWireGuardEndpointJsonForServer builds a compliant Sing-Box WireGuard endpoint for a physical server
func BuildWireGuardEndpointJsonForServer(tag string, server *PhysicalServerEntry, clientPrivateKey string) (json.RawMessage, error) {
	if clientPrivateKey == "" {
		clientPrivateKey = "yBVl8qcgy/OTwV7fZ4bQzeQv5OAR3AJ2C583nN5u218="
	}
	port := server.Port
	if port <= 0 {
		port = 51820
	}
	peers := []map[string]interface{}{
		{
			"address":                       server.EntryIP,
			"port":                          port,
			"public_key":                    server.PublicKey,
			"allowed_ips":                   []string{"0.0.0.0/0", "::/0"},
			"persistent_keepalive_interval": 25,
		},
	}
	epMap := map[string]interface{}{
		"type":        "wireguard",
		"tag":         tag,
		"system":      false,
		"address":     []string{"10.2.0.2/32", "2a07:b944::2:2/128"},
		"private_key": clientPrivateKey,
		"listen_port": 0,
		"peers":       peers,
	}
	return json.Marshal(epMap)
}

// GetBestServer returns the single highest ranked physical server for a given country
func (c *MultiCountryCache) GetBestServer(countryCode string) *PhysicalServerEntry {
	servers := c.GetCountryServers(countryCode)
	if len(servers) > 0 {
		return servers[0]
	}
	return nil
}

// GetAllCountries returns a sorted slice of all country codes currently held in cache
func (c *MultiCountryCache) GetAllCountries() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	countries := make([]string, 0, len(c.serversByLoc))
	for k := range c.serversByLoc {
		countries = append(countries, k)
	}
	sort.Strings(countries)
	return countries
}

// CacheInfo returns metadata about the current cache status
func (c *MultiCountryCache) CacheInfo() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	totalServers := 0
	for _, l := range c.serversByLoc {
		totalServers += len(l)
	}

	return map[string]interface{}{
		"country_count": len(c.serversByLoc),
		"server_count":  totalServers,
		"last_updated":  c.lastUpdated.Format(time.RFC3339),
		"source_path":   c.activeSource,
	}
}

// UpdateFromRemote attempts to fetch fresh logicals from remote API and persist to disk
func (c *MultiCountryCache) UpdateFromRemote(ctx context.Context) error {
	client := &http.Client{Timeout: 25 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://account.proton.me/api/vpn/logicals", nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/vnd.protonmail.v1+json")
	req.Header.Set("x-pm-appversion", "linux-vpn@4.14.1")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("remote API query failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("remote API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var apiResp struct {
		Code           int                    `json:"Code"`
		LogicalServers []*ProtonLogicalServer `json:"LogicalServers"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return fmt.Errorf("unmarshal error: %w", err)
	}

	if len(apiResp.LogicalServers) < 50 {
		return fmt.Errorf("received too few logical servers (%d), aborting update to protect cache", len(apiResp.LogicalServers))
	}

	// Persist to the preferred candidate path
	targetPath := candidateCachePaths()[0]
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err == nil {
		if writeErr := os.WriteFile(targetPath, body, 0644); writeErr == nil {
			logger.Infof("Successfully refreshed and wrote %d logical servers to %s", len(apiResp.LogicalServers), targetPath)
		}
	}

	// Reload memory registry
	return c.ReloadFromDisk()
}

// StartAutoUpdater spawns a background goroutine to periodically reload or refresh the cache
func (c *MultiCountryCache) StartAutoUpdater(interval time.Duration) {
	if interval <= 0 {
		interval = 12 * time.Hour
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := c.UpdateFromRemote(ctx); err != nil {
				// Fallback reload from disk if remote update fails
				_ = c.ReloadFromDisk()
			}
			cancel()
		}
	}()
}
