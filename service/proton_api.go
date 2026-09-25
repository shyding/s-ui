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
	"runtime"
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

// InferCountry infers ISO 2-letter country code from filename, path, or config content comments
func InferCountry(name string, content string) string {
	combined := strings.ToUpper(name + "\n" + content)

	// Check explicit country codes with word boundaries in filename
	countryRe := regexp.MustCompile(`(?i)(?:^|[^a-zA-Z])(US|JP|NL|SG|HK|UK|GB|DE|CA|AU|FR|CH)(?:[^a-zA-Z]|$)`)
	if match := countryRe.FindStringSubmatch(strings.ToUpper(name)); len(match) > 1 {
		code := strings.ToUpper(match[1])
		if code == "UK" {
			code = "GB"
		}
		return code
	}

	// Keywords in path or content
	if strings.Contains(combined, "JAPAN") || strings.Contains(combined, "TOKYO") {
		return "JP"
	}
	if strings.Contains(combined, "NETHERLANDS") || strings.Contains(combined, "AMSTERDAM") {
		return "NL"
	}
	if strings.Contains(combined, "SINGAPORE") {
		return "SG"
	}
	if strings.Contains(combined, "HONG KONG") || strings.Contains(combined, "HONGKONG") {
		return "HK"
	}
	if strings.Contains(combined, "GERMANY") || strings.Contains(combined, "FRANKFURT") {
		return "DE"
	}
	if strings.Contains(combined, "CANADA") {
		return "CA"
	}
	if strings.Contains(combined, "AUSTRALIA") || strings.Contains(combined, "SYDNEY") {
		return "AU"
	}
	if strings.Contains(combined, "FRANCE") || strings.Contains(combined, "PARIS") {
		return "FR"
	}
	if strings.Contains(combined, "SWITZERLAND") || strings.Contains(combined, "ZURICH") {
		return "CH"
	}
	if strings.Contains(combined, "UNITED STATES") || strings.Contains(combined, "AMERICA") || strings.Contains(combined, "NEW YORK") || strings.Contains(combined, "LOS ANGELES") {
		return "US"
	}

	// Check comments in content (e.g. # US-FREE#3 or # NL-FREE#5)
	if match := countryRe.FindStringSubmatch(strings.ToUpper(content)); len(match) > 1 {
		code := strings.ToUpper(match[1])
		if code == "UK" {
			code = "GB"
		}
		return code
	}

	return "US"
}

// SplitWireGuardConfigs splits multiple [Interface] blocks into distinct configuration strings
func SplitWireGuardConfigs(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")

	if !strings.Contains(strings.ToLower(content), "[interface]") {
		if strings.TrimSpace(content) != "" {
			return []string{content}
		}
		return nil
	}

	re := regexp.MustCompile(`(?i)\[interface\]`)
	indices := re.FindAllStringIndex(content, -1)
	if len(indices) == 0 {
		return []string{content}
	}

	var configs []string
	for i := 0; i < len(indices); i++ {
		start := indices[i][0]
		var end int
		if i+1 < len(indices) {
			end = indices[i+1][0]
		} else {
			end = len(content)
		}
		block := strings.TrimSpace(content[start:end])
		if block != "" {
			configs = append(configs, block)
		}
	}
	return configs
}

// UploadedConf represents an uploaded file or text block
type UploadedConf struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	Country string `json:"country,omitempty"`
}

// ImportWireGuardConfsData parses multiple uploaded or pasted WireGuard configs,
// groups them by inferred country, and imports them into S-UI database and failover pools.
func ImportWireGuardConfsData(db *gorm.DB, items []UploadedConf, defaultCountry string) (map[string]int, []string, error) {
	if defaultCountry == "" {
		defaultCountry = "US"
	}
	defaultCountry = strings.ToUpper(strings.TrimSpace(defaultCountry))

	results := make(map[string]int)
	var importedTags []string
	countryConfigs := make(map[string][]*WireGuardConf)

	for _, item := range items {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}

		subBlocks := SplitWireGuardConfigs(content)
		for idx, block := range subBlocks {
			conf, err := ParseWireGuardConf(block)
			if err != nil {
				logger.Warningf("Failed to parse WireGuard config %s (block %d): %v", item.Name, idx, err)
				continue
			}

			country := strings.ToUpper(strings.TrimSpace(item.Country))
			if country == "" || country == "AUTO" {
				country = InferCountry(item.Name, block)
			}
			if country == "" {
				country = defaultCountry
			}

			name := strings.TrimSpace(item.Name)
			if name == "" {
				name = fmt.Sprintf("%s-%s", country, conf.ServerIP)
			} else {
				name = strings.TrimSuffix(name, filepath.Ext(name))
				if len(subBlocks) > 1 {
					name = fmt.Sprintf("%s-%d", name, idx+1)
				}
			}

			conf.Name = name
			conf.Country = country
			countryConfigs[country] = append(countryConfigs[country], conf)
		}
	}

	if len(countryConfigs) == 0 {
		return results, importedTags, fmt.Errorf("未从上传内容中解析到有效的 WireGuard 配置，请确保包含 [Interface] 与 [Peer] 字段")
	}

	for country, configs := range countryConfigs {
		count, err := BatchImportWireGuardToSUI(db, configs, country)
		if err == nil && count > 0 {
			results[country] = count
			for _, c := range configs {
				cleanName := SanitizeTag(c.Name)
				importedTags = append(importedTags, fmt.Sprintf("out-proton-%s", cleanName))
			}
		}
	}

	return results, importedTags, nil
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

				country := InferCountry(e.Name(), string(content))
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

// loadCachedLogicals tries to load pre-harvested Proton server list from cached JSON
func loadCachedLogicals() []*ProtonLogicalServer {
	candidates := []string{
		filepath.Join("scripts", "cached_logicals.json"),
		filepath.Join("/usr/local/s-ui", "scripts", "cached_logicals.json"),
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "scripts", "cached_logicals.json"))
	}
	candidates = append(candidates, filepath.Join("i:", "learn_code", "s-ui", "scripts", "cached_logicals.json"))

	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err == nil && len(data) > 0 {
			var servers []*ProtonLogicalServer
			if err := json.Unmarshal(data, &servers); err == nil && len(servers) > 0 {
				logger.Infof("Loaded %d Proton servers from local cache %s", len(servers), p)
				return servers
			}
		}
	}
	return nil
}

// HarvestProtonNodesViaBrowser executes the automated browser harvester to fetch servers
// with ZERO manual token/cookie copy-paste. Supports username/password automated login.
func HarvestProtonNodesViaBrowser(db *gorm.DB, username string, password string, headless bool, scriptPath string, countries ...string) (int, string, error) {
	pythonBin := "python3"
	if runtime.GOOS == "windows" {
		pythonBin = "python.exe"
	}
	hasPython := false
	if _, err := exec.LookPath(pythonBin); err == nil {
		hasPython = true
	} else if runtime.GOOS == "windows" {
		if _, err2 := exec.LookPath("python"); err2 == nil {
			pythonBin = "python"
			hasPython = true
		}
	}

	if scriptPath == "" {
		candidates := []string{
			filepath.Join("scripts", "proton_harvester.py"),
			filepath.Join("/usr/local/s-ui", "scripts", "proton_harvester.py"),
		}
		if exe, err := os.Executable(); err == nil {
			candidates = append(candidates, filepath.Join(filepath.Dir(exe), "scripts", "proton_harvester.py"))
		}
		candidates = append(candidates, filepath.Join("i:", "learn_code", "s-ui", "scripts", "proton_harvester.py"))

		for _, cand := range candidates {
			if _, err := os.Stat(cand); err == nil {
				scriptPath = cand
				break
			}
		}
	}

	var res HarvestResult
	hasHarvested := false

	// Attempt live harvest if python and script are available
	if hasPython && scriptPath != "" {
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

		cmd := exec.Command(pythonBin, args...)
		outputBytes, _ := cmd.CombinedOutput()
		outputStr := string(outputBytes)

		startMarker := "---SUI_HARVEST_START---"
		endMarker := "---SUI_HARVEST_END---"
		startIndex := strings.Index(outputStr, startMarker)
		endIndex := strings.Index(outputStr, endMarker)

		if startIndex != -1 && endIndex != -1 && endIndex > startIndex {
			jsonStr := strings.TrimSpace(outputStr[startIndex+len(startMarker) : endIndex])
			if err := json.Unmarshal([]byte(jsonStr), &res); err == nil && res.Success && len(res.Servers) > 0 {
				hasHarvested = true
			}
		}
	}

	// Fallback to cached logicals if live harvest did not yield servers
	if !hasHarvested || len(res.Servers) == 0 {
		if cached := loadCachedLogicals(); len(cached) > 0 {
			res.Success = true
			res.Servers = cached
			res.Message = fmt.Sprintf("成功从本地加速缓存提取 %d 个 ProtonVPN 节点", len(cached))
			hasHarvested = true
		}
	}

	if !hasHarvested || len(res.Servers) == 0 {
		return 0, "", fmt.Errorf("未能获取到 ProtonVPN 节点。建议使用【文件上传】直接选取本地 .conf 文件秒级导入！")
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

