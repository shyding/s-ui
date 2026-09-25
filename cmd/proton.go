package cmd

import (
	"fmt"
	"strings"

	"github.com/alireza0/s-ui/config"
	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/service"
)

func runProtonCmd(importDir string, token string, uid string, privKey string, countries string, browser bool, auto bool) {
	err := database.InitDB(config.GetDBPath())
	if err != nil {
		fmt.Printf("Database init failed: %v\n", err)
		return
	}
	db := database.GetDB()
	countryList := strings.Split(countries, ",")

	if browser || auto {
		headless := auto && !browser
		modeStr := "headless background"
		if !headless {
			modeStr = "interactive simulated browser"
		}
		fmt.Printf("Launching %s harvester (Zero manual token/cookie required)...\n", modeStr)

		count, msg, err := service.HarvestProtonNodesViaBrowser(db, headless, "", countryList...)
		if err != nil {
			fmt.Printf("Harvest error: %v\nDetails: %s\n", err, msg)
			return
		}
		fmt.Printf("Harvest completed successfully! %s\nTotal imported nodes: %d\n", msg, count)
		return
	}

	if importDir != "" {
		fmt.Printf("Scanning directory: %s ...\n", importDir)
		results, err := service.ScanAndImportProtonDirectory(db, importDir)
		if err != nil {
			fmt.Printf("Scan and import failed: %v\n", err)
			return
		}
		total := 0
		for country, count := range results {
			fmt.Printf("Country [%s]: Imported %d nodes\n", country, count)
			total += count
		}
		fmt.Printf("Total %d ProtonVPN nodes successfully imported into S-UI pools!\n", total)
		return
	}

	if token != "" {
		fmt.Printf("Connecting to ProtonVPN API with simulated browser headers...\n")
		servers, err := service.FetchProtonLogicalServers(token, uid, "linux-vpn@4.14.1")
		if err != nil {
			fmt.Printf("Fetch failed: %v\n", err)
			return
		}

		freeServers := service.FilterFreeLogicalServers(servers, countryList...)
		fmt.Printf("Discovered %d free servers matching countries: %s\n", len(freeServers), countries)

		if privKey == "" {
			priv, _, err := service.GenerateNewWireGuardKeyPair()
			if err != nil {
				fmt.Printf("Failed to generate client keypair: %v\n", err)
				return
			}
			privKey = priv
		}

		configs := service.ConvertToWireGuardConfigs(freeServers, privKey, nil)
		countryMap := make(map[string][]*service.WireGuardConf)
		for _, conf := range configs {
			countryMap[conf.Country] = append(countryMap[conf.Country], conf)
		}

		total := 0
		for country, cConfigs := range countryMap {
			count, err := service.BatchImportWireGuardToSUI(db, cConfigs, country)
			if err == nil {
				fmt.Printf("Country [%s]: Imported %d nodes into %s-pool\n", country, count, strings.ToLower(country))
				total += count
			}
		}
		fmt.Printf("Successfully pulled and imported %d ProtonVPN free nodes into S-UI!\n", total)
		return
	}

	fmt.Println("Usage: sui proton [-browser | -auto | -import-dir <path> | -token <token>]")
	fmt.Println("  -browser      Launch simulated browser window to log in and harvest nodes (Zero manual copy-paste)")
	fmt.Println("  -auto         Silently harvest nodes in the background using saved session")
	fmt.Println("  -import-dir   Recursively scan and import .conf WireGuard files from local directory")
	fmt.Println("  -countries    Target country codes (default: US,JP,NL)")
}
