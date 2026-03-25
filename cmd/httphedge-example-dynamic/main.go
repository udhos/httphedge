// Package main implements the example.
package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/udhos/httphedge/httphedge"
	"github.com/udhos/rollingpercentile/rollingpercentile"
)

func main() {
	ctx := context.Background()

	// 1. Configure the rolling percentile options.
	// This tells the tracker to look at a 1-minute window and
	// calculate the 95th percentile (P95) latency.
	opts := rollingpercentile.Options{
		TotalWindow: time.Minute,
		Percentile:  95.0,
	}

	// 2. Create the DynamicProvider.
	// We set a minDelay of 10ms to prevent "instant" hedges
	// before the tracker has enough data.
	provider := httphedge.NewDynamicProvider(ctx, opts, 10*time.Millisecond)

	// 3. Wrap your standard client.
	standardClient := &http.Client{Timeout: 5 * time.Second}
	client := httphedge.WrapDoer(standardClient, provider, 2)

	// 4. Execute requests.
	// The library will record the latency of each success and
	// adjust the hedging delay for the next request.
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://google.com", nil)

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer resp.Body.Close()

	fmt.Printf("Request finished with Status: %s\n", resp.Status)
}
