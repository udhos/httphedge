// Package main implements the example.
package main

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/udhos/httphedge/httphedge"
)

func main() {
	// 1. Create a standard http.Client
	standardClient := &http.Client{Timeout: 10 * time.Second}

	// 2. Define a hedging strategy (e.g., hedge if first req takes > 200ms)
	provider := httphedge.StaticProvider(200 * time.Millisecond)

	// 3. Wrap the client to create a hedged HTTPDoer.
	// We allow a maximum of 2 attempts (1 original + 1 hedge).
	client := httphedge.WrapDoer(standardClient, provider, 2)

	// 4. Use it just like a normal client
	req, _ := http.NewRequest(http.MethodGet, "https://www.google.com", nil)

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Request failed: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("Status: %s, Body length: %d\n", resp.Status, len(body))
}
