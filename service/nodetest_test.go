package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestExecuteIPLookups_ErrorAggregation(t *testing.T) {
	s := &NodeTestService{}
	
	baseResult := &NodeTestResult{
		Tag:       "test-node",
		Server:    "127.0.0.1",
		Port:      1080,
		Available: true,
	}

	// Create 4 mock tasks that all fail with specific errors
	tasks := []IPLookupTask{
		func(ctx context.Context, res *NodeTestResult) error {
			return fmt.Errorf("ip-api: timeout")
		},
		func(ctx context.Context, res *NodeTestResult) error {
			return fmt.Errorf("ipinfo: connection refused")
		},
		func(ctx context.Context, res *NodeTestResult) error {
			return fmt.Errorf("ipwhois: EOF")
		},
		func(ctx context.Context, res *NodeTestResult) error {
			return fmt.Errorf("ping0: tls: internal error")
		},
	}

	ctx := context.Background()
	s.executeIPLookups(ctx, baseResult, tasks)

	if baseResult.LandingIP != "" {
		t.Errorf("Expected LandingIP to be empty, got %s", baseResult.LandingIP)
	}

	// Since all failed, Error should contain at least parts of the collected errors
	if baseResult.Error == "" {
		t.Errorf("Expected Error to be aggregated, got empty string")
	} else {
		expectedErrors := []string{"Timeout", "Connection Refused", "Connection Closed (EOF)", "TLS Handshake"}
		for _, errText := range expectedErrors {
			if !strings.Contains(baseResult.Error, errText) {
				t.Errorf("Expected Error to contain %q, but got: %s", errText, baseResult.Error)
			}
		}
	}
}

func TestExecuteIPLookups_SuccessEndsEarly(t *testing.T) {
	s := &NodeTestService{}
	
	baseResult := &NodeTestResult{
		Tag:       "test-node",
		Server:    "127.0.0.1",
		Port:      1080,
		Available: true,
	}

	tasks := []IPLookupTask{
		func(ctx context.Context, res *NodeTestResult) error {
			return fmt.Errorf("ip-api: timeout")
		},
		func(ctx context.Context, res *NodeTestResult) error {
			res.LandingIP = "8.8.8.8"
			res.Country = "US"
			return nil // Success
		},
		func(ctx context.Context, res *NodeTestResult) error {
			// This might run but result won't be used
			return fmt.Errorf("ipwhois: EOF")
		},
	}

	ctx := context.Background()
	s.executeIPLookups(ctx, baseResult, tasks)

	if baseResult.LandingIP != "8.8.8.8" {
		t.Errorf("Expected LandingIP to be 8.8.8.8, got %s", baseResult.LandingIP)
	}

	if baseResult.Error != "" {
		t.Errorf("Expected Error to be empty, got %s", baseResult.Error)
	}
}

func TestSimplifyError(t *testing.T) {
	s := &NodeTestService{}
	
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    `request failed: Get "http://ipwhois.app/json/": dial udp [2001:41d0:701:1100::64a2]:5021: connect: A socket operation was attempted to an unreachable network.`,
			expected: "Network Unreachable",
		},
		{
			input:    `dial tcp: lookup v6-aws-hk.aikunapp.com: no such host`,
			expected: "DNS Resolution Failed",
		},
		{
			input:    `request failed: Get "http://ping0.cc/geo": context deadline exceeded`,
			expected: "Timeout",
		},
		{
			input:    `request failed: Get "http://ip-api.com/json": dial tcp 1.1.1.1:80: i/o timeout`,
			expected: "Timeout",
		},
		{
			input:    `request failed: Get "http://ipinfo.io/json": EOF`,
			expected: "Connection Closed (EOF)",
		},
		{
			input:    `dial tcp 127.0.0.1:1080: connect: connection refused`,
			expected: "Connection Refused",
		},
		{
			input:    `request failed: Get "https://ipwhois.app/json/": x509: certificate signed by unknown authority`,
			expected: "TLS Certificate Error",
		},
		{
			input:    `request failed: Get "https://ipwhois.app/json/": tls: internal error`,
			expected: "TLS Handshake Failed",
		},
		{
			input:    `some generic error`,
			expected: "some generic error",
		},
	}

	for _, tc := range tests {
		actual := s.simplifyError(tc.input)
		if actual != tc.expected {
			t.Errorf("simplifyError(%q) = %q; expected %q", tc.input, actual, tc.expected)
		}
	}
}
