package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	url := "https://138.128.194.174/"
	fmt.Printf("Fetching %s...\n", url)

	// 1. Try with default client (should fail if self-signed)
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	_, err := client.Get(url)
	if err != nil {
		fmt.Printf("Default client failed: %v\n", err)
	} else {
		fmt.Println("Default client succeeded (Unexpected for self-signed IP cert)")
	}

	// 2. Try with insecure skip verify
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	insecureClient := &http.Client{
		Transport: tr,
		Timeout:   10 * time.Second,
	}
	resp, err := insecureClient.Get(url)
	if err != nil {
		fmt.Printf("Insecure client failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("Insecure client succeeded. Content length: %d\n", len(body))
	fmt.Printf("First 100 chars: %s\n", string(body[:min(len(body), 100)]))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
