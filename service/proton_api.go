package service

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/alireza0/s-ui/database/model"
	"github.com/alireza0/s-ui/logger"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"gorm.io/gorm"
)

// ProtonLogicalResponse represents the response envelope from Proton logicals endpoint
type ProtonLogicalResponse struct {
	Code           int                    `json:"Code"`
	LogicalServers []*ProtonLogicalServer `json:"LogicalServers"`
	Error          string                 `json:"Error,omitempty"`
}

// ProtonLogicalServer represents a logical server cluster in ProtonVPN
type ProtonLogicalServer struct {
	ID           string          `json:"ID"`
	Name         string          `json:"Name"`         // e.g. "US-FREE#9"
	EntryCountry string          `json:"EntryCountry"` // e.g. "US"
	ExitCountry  string          `json:"ExitCountry"`  // e.g. "US"
	Domain       string          `json:"Domain"`
	Tier         int             `json:"Tier"`         // 0 = Free, 1 = Basic, 2 = Plus
	Features     int             `json:"Features"`
	Load         int             `json:"Load"`         // Current load percentage (0-100)
	Score        float64         `json:"Score"`
	Status       int             `json:"Status"`       // 1 = Online, 0 = Offline
	Servers      []*ProtonServer `json:"Servers"`
}

// ProtonServer represents a physical server instance
type ProtonServer struct {
	ID              string `json:"ID"`
	EntryIP         string `json:"EntryIP"`         // Endpoint IP for WireGuard
	ExitIP          string `json:"ExitIP"`
	Domain          string `json:"Domain"`
	Status          int    `json:"Status"`
	X25519PublicKey string `json:"X25519PublicKey"` // Server's WireGuard public key
}

// GenerateNewWireGuardKeyPair generates a cryptographically secure WireGuard key pair
func GenerateNewWireGuardKeyPair() (privateKeyBase64 string, publicKeyBase64 string, err error) {
	key, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		return "", "", err
	}
	return key.String(), key.PublicKey().String(), nil
}

// FetchProtonLogicalServers fetches the raw server list from Proton's API using browser simulation
func FetchProtonLogicalServers(accessToken string, uid string, appVersion string) ([]*ProtonLogicalServer, error) {
	if appVersion == "" {
		appVersion = "linux-vpn@4.14.1"
	}

	endpoints := []string{
		"https://account.proton.me/api/vpn/logicals",
		"https://vpn-api.proton.me/vpn/logicals",
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   20 * time.Second,
	}

	var lastErr error
	for _, url := range endpoints {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			lastErr = err
			continue
		}

		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "application/vnd.protonmail.v1+json")
		req.Header.Set("x-pm-appversion", appVersion)
		if uid != "" {
			req.Header.Set("x-pm-uid", uid)
		}
		if accessToken != "" {
			if !strings.HasPrefix(accessToken, "Bearer ") {
				req.Header.Set("Authorization", "Bearer "+accessToken)
			} else {
				req.Header.Set("Authorization", accessToken)
			}
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode == 401 {
			return nil, fmt.Errorf("proton api authentication failed (401 Unauthorized): invalid or expired session token/uid")
		}

		if resp.StatusCode != 200 {
			lastErr = fmt.Errorf("proton api returned HTTP %d: %s", resp.StatusCode, string(body))
			continue
		}

		var apiResp ProtonLogicalResponse
		if err := json.Unmarshal(body, &apiResp); err != nil {
			lastErr = fmt.Errorf("failed to unmarshal logicals response: %v", err)
			continue
		}

		if apiResp.Code != 1000 && apiResp.Error != "" {
			lastErr = fmt.Errorf("proton api error code %d: %s", apiResp.Code, apiResp.Error)
			continue
		}

		return apiResp.LogicalServers, nil
	}

	return nil, fmt.Errorf("all proton api endpoints failed: %v", lastErr)
}

// FilterFreeLogicalServers filters logical servers by free tier (Tier == 0) and optional countries
func FilterFreeLogicalServers(servers []*ProtonLogicalServer, countries ...string) []*ProtonLogicalServer {
	countryMap := make(map[string]bool)
	for _, c := range countries {
		countryMap[strings.ToUpper(strings.TrimSpace(c))] = true
	}

	var filtered []*ProtonLogicalServer
	for _, s := range servers {
		// Tier 0 is Free
		if s.Tier == 0 && s.Status == 1 {
			if len(countryMap) == 0 || countryMap[strings.ToUpper(s.ExitCountry)] {
				filtered = append(filtered, s)
			}
		}
	}

	// Sort by lowest load first
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Load < filtered[j].Load
	})

	return filtered
}

// ConvertToWireGuardConfigs converts Proton free logical servers to WireGuardConf models
func ConvertToWireGuardConfigs(servers []*ProtonLogicalServer, clientPrivateKey string, clientAddress []string) []*WireGuardConf {
	if len(clientAddress) == 0 {
		clientAddress = []string{"10.2.0.2/32", "2a07:b944::2:2/128"}
	}

	var configs []*WireGuardConf
	for _, s := range servers {
		if s.Status != 1 || len(s.Servers) == 0 {
			continue
		}
		phys := s.Servers[0]
		if phys.EntryIP == "" || phys.X25519PublicKey == "" {
			continue
		}

		conf := &WireGuardConf{
			Name:       s.Name,
			Country:    s.ExitCountry,
			Load:       s.Load,
			PrivateKey: clientPrivateKey,
			Address:    clientAddress,
			PublicKey:  phys.X25519PublicKey,
			Endpoint:   fmt.Sprintf("%s:51820", phys.EntryIP),
			ServerIP:   phys.EntryIP,
			ServerPort: 51820,
			Keepalive:  25,
		}
		configs = append(configs, conf)
	}
	return configs
}

// SanitizeTag creates a safe Sing-Box tag string (lowercase, alphanumeric + hyphen)
func SanitizeTag(name string) string {
	reg := regexp.MustCompile(`[^a-zA-Z0-9]+`)
	safe := reg.ReplaceAllString(strings.ToLower(name), "-")
	return strings.Trim(safe, "-")
}

// BatchImportWireGuardToSUI persists a slice of WireGuard configurations into S-UI database
// and orchestrates them into a URLTest auto-failover pool for that country
func BatchImportWireGuardToSUI(db *gorm.DB, configs []*WireGuardConf, countryCode string) (int, error) {
	if len(configs) == 0 {
		return 0, nil
	}
	countryCode = strings.ToLower(strings.TrimSpace(countryCode))
	poolTag := fmt.Sprintf("%s-pool", countryCode)

	var outboundTags []string
	importedCount := 0

	for _, conf := range configs {
		if conf.ServerIP == "" || conf.PublicKey == "" || conf.PrivateKey == "" {
			continue
		}

		nodeName := conf.Name
		if nodeName == "" {
			nodeName = fmt.Sprintf("%s-%s", countryCode, conf.ServerIP)
		}
		cleanName := SanitizeTag(nodeName)
		epTag := fmt.Sprintf("ep-proton-%s", cleanName)
		outTag := fmt.Sprintf("out-proton-%s", cleanName)

		// 1. Check or Upsert Endpoint
		epJson, err := BuildWireGuardEndpointJson(epTag, conf)
		if err != nil {
			logger.Warningf("Failed to build endpoint json for %s: %v", epTag, err)
			continue
		}

		var existingEp model.Endpoint
		if err := db.Where("tag = ?", epTag).First(&existingEp).Error; err != nil {
			// Create new
			newEp := model.Endpoint{
				Type: "wireguard",
				Tag:  epTag,
			}
			_ = newEp.UnmarshalJSON(epJson)
			if err := db.Create(&newEp).Error; err != nil {
				logger.Warningf("Failed to insert endpoint %s: %v", epTag, err)
				continue
			}
		} else {
			// Update options
			_ = existingEp.UnmarshalJSON(epJson)
			db.Save(&existingEp)
		}

		// 2. Check or Upsert Outbound
		outJson, err := BuildDirectOutboundJson(outTag, epTag)
		if err != nil {
			continue
		}

		var existingOut model.Outbound
		if err := db.Where("tag = ?", outTag).First(&existingOut).Error; err != nil {
			newOut := model.Outbound{
				Type:      "direct",
				Tag:       outTag,
				Country:   conf.Country,
				LandingIP: conf.ServerIP,
				Available: true,
			}
			_ = newOut.UnmarshalJSON(outJson)
			if err := db.Create(&newOut).Error; err != nil {
				continue
			}
		} else {
			existingOut.Country = conf.Country
			existingOut.LandingIP = conf.ServerIP
			existingOut.Available = true
			_ = existingOut.UnmarshalJSON(outJson)
			db.Save(&existingOut)
		}

		outboundTags = append(outboundTags, outTag)
		importedCount++
	}

	// 3. Update or Create URLTest Pool
	if len(outboundTags) > 0 {
		poolJson, err := BuildUrlTestPoolJson(poolTag, outboundTags, "3m")
		if err == nil {
			var poolOut model.Outbound
			if err := db.Where("tag = ?", poolTag).First(&poolOut).Error; err != nil {
				newPool := model.Outbound{
					Type:      "urltest",
					Tag:       poolTag,
					Country:   strings.ToUpper(countryCode),
					Available: true,
				}
				_ = newPool.UnmarshalJSON(poolJson)
				db.Create(&newPool)
			} else {
				poolOut.Type = "urltest"
				poolOut.Country = strings.ToUpper(countryCode)
				_ = poolOut.UnmarshalJSON(poolJson)
				db.Save(&poolOut)
			}
		}
	}

	return importedCount, nil
}

// ScanAndImportProtonDirectory scans a directory recursively for all .conf files and imports them
func ScanAndImportProtonDirectory(db *gorm.DB, dirPath string) (map[string]int, error) {
	results := make(map[string]int)

	countryConfigs := make(map[string][]*WireGuardConf)

	var scanDir func(path string)
	scanDir = func(currentDir string) {
		dirEntries, err := os.ReadDir(currentDir)
		if err != nil {
			return
		}
		for _, e := range dirEntries {
			fullPath := filepath.Join(currentDir, e.Name())
			if e.IsDir() {
				scanDir(fullPath)
			} else if strings.HasSuffix(strings.ToLower(e.Name()), ".conf") {
				content, err := os.ReadFile(fullPath)
				if err != nil {
					continue
				}
				conf, err := ParseWireGuardConf(string(content))
				if err != nil {
					continue
				}

				// Infer country from file name or directory path
				baseNameUpper := strings.ToUpper(e.Name())
				fullPathUpper := strings.ToUpper(fullPath)
				country := "US"

				countryRe := regexp.MustCompile(`(?i)(?:^|[^a-zA-Z])(US|JP|NL|SG)(?:[^a-zA-Z]|$)`)
				if match := countryRe.FindStringSubmatch(baseNameUpper); len(match) > 1 {
					country = strings.ToUpper(match[1])
				} else if strings.Contains(fullPathUpper, "\\US\\") || strings.Contains(fullPathUpper, "/US/") {
					country = "US"
				} else if strings.Contains(fullPathUpper, "\\JP\\") || strings.Contains(fullPathUpper, "/JP/") || strings.Contains(fullPathUpper, "JAPAN") {
					country = "JP"
				} else if strings.Contains(fullPathUpper, "\\NL\\") || strings.Contains(fullPathUpper, "/NL/") || strings.Contains(fullPathUpper, "NETHERLANDS") {
					country = "NL"
				} else if strings.Contains(fullPathUpper, "\\SG\\") || strings.Contains(fullPathUpper, "/SG/") || strings.Contains(fullPathUpper, "SINGAPORE") {
					country = "SG"
				}

				conf.Name = strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
				conf.Country = country
				countryConfigs[country] = append(countryConfigs[country], conf)
			}
		}
	}

	scanDir(dirPath)

	for c, configs := range countryConfigs {
		count, err := BatchImportWireGuardToSUI(db, configs, c)
		if err == nil {
			results[c] = count
		}
	}

	return results, nil
}

// HarvestResult represents the JSON payload from the automated browser harvester
type HarvestResult struct {
	Success bool                   `json:"success"`
	Servers []*ProtonLogicalServer `json:"servers"`
	Message string                 `json:"message"`
}

// HarvestProtonNodesViaBrowser executes the automated browser harvester to fetch servers
// with ZERO manual token/cookie copy-paste. Supports username/password automated login.
func HarvestProtonNodesViaBrowser(db *gorm.DB, username string, password string, headless bool, scriptPath string, countries ...string) (int, string, error) {
	if scriptPath == "" {
		scriptPath = filepath.Join("scripts", "proton_harvester.py")
		if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
			scriptPath = filepath.Join("i:", "learn_code", "s-ui", "scripts", "proton_harvester.py")
		}
	}

	args := []string{scriptPath}
	if headless {
		args = append(args, "--headless")
	}
	if username != "" {
		args = append(args, "--username", username)
	}
	if password != "" {
		args = append(args, "--password", password)
	}

	cmd := exec.Command("python.exe", args...)
	outputBytes, err := cmd.CombinedOutput()
	outputStr := string(outputBytes)

	startMarker := "---SUI_HARVEST_START---"
	endMarker := "---SUI_HARVEST_END---"
	startIndex := strings.Index(outputStr, startMarker)
	endIndex := strings.Index(outputStr, endMarker)

	var jsonStr string
	if startIndex != -1 && endIndex != -1 && endIndex > startIndex {
		jsonStr = strings.TrimSpace(outputStr[startIndex+len(startMarker) : endIndex])
	} else {
		return 0, outputStr, fmt.Errorf("harvester output missing result markers: %s", outputStr)
	}

	var res HarvestResult
	if err := json.Unmarshal([]byte(jsonStr), &res); err != nil {
		return 0, outputStr, fmt.Errorf("failed to parse harvester result JSON: %v, raw: %s", err, jsonStr)
	}

	if !res.Success || len(res.Servers) == 0 {
		return 0, res.Message, fmt.Errorf("harvest unsuccessful: %s", res.Message)
	}

	if len(countries) == 0 {
		countries = []string{"US", "JP", "NL"}
	}

	freeServers := FilterFreeLogicalServers(res.Servers, countries...)
	if len(freeServers) == 0 {
		return 0, fmt.Sprintf("Found %d servers but no free servers matched countries %v", len(res.Servers), countries), nil
	}

	privKey, _, err := GenerateNewWireGuardKeyPair()
	if err != nil {
		return 0, "", fmt.Errorf("failed to generate WireGuard keypair: %v", err)
	}

	configs := ConvertToWireGuardConfigs(freeServers, privKey, nil)
	countryMap := make(map[string][]*WireGuardConf)
	for _, conf := range configs {
		countryMap[conf.Country] = append(countryMap[conf.Country], conf)
	}

	totalImported := 0
	for country, cConfigs := range countryMap {
		count, err := BatchImportWireGuardToSUI(db, cConfigs, country)
		if err == nil {
			totalImported += count
		}
	}

	return totalImported, fmt.Sprintf("Successfully harvested and imported %d ProtonVPN free nodes into S-UI pools", totalImported), nil
}

