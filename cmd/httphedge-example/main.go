// Package main implements an example.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/udhos/httphedge/httphedge"
	"github.com/udhos/rollingpercentile/rollingpercentile"
)

func main() {
	var useDynamic bool
	flag.BoolVar(&useDynamic, "dynamic", false, "use dynamic percentile-based hedging")
	flag.Parse()

	fmt.Printf("dynamic: %t\n", useDynamic)

	ctx := context.Background()
	var provider httphedge.DelayProvider

	if useDynamic {
		fmt.Println("Mode: Dynamic (Adaptive P95)")
		opts := rollingpercentile.Options{
			TotalWindow:          5 * time.Minute,
			Percentile:           90.0,
			MaxLatencyMs:         30000,
			CacheRefreshInterval: 5 * time.Second,
		}
		// Initialize with a 10ms floor.
		provider = httphedge.NewDynamicProvider(ctx, opts, 10*time.Millisecond)
	} else {
		fmt.Println("Mode: Static (Fixed 200ms)")
		provider = httphedge.StaticProvider(200 * time.Millisecond)
	}

	standardClient := http.DefaultClient
	standardClient.Timeout = 10 * time.Second

	// Upgrade the standard client with hedging logic.
	// We allow a maximum of 3 attempts (1 original + 2 hedges).
	client := httphedge.WrapDoer(http.DefaultClient, provider, 3)

	req, err := http.NewRequest(http.MethodGet, "https://www.google.com", nil)
	if err != nil {
		fmt.Printf("Error creating request: %v\n", err)
		return
	}

	fmt.Println("Executing request with hedging (delay: 200ms)...")
	start := time.Now()

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Request failed after hedging: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("Response Status: %s\n", resp.Status)
	fmt.Printf("Total Duration: %v\n", time.Since(start))
	fmt.Printf("Body length: %d bytes\n", len(body))
}
