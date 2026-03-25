package httphedge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/udhos/rollingpercentile/rollingpercentile"
)

func TestWrapDoer_ImprovedLatency(t *testing.T) {
	var requestCount atomic.Int32

	// 1. Create a server that is very slow on the first call, fast on the second.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count := requestCount.Add(1)
		if count == 1 {
			// First request is the "tail latency" instance
			time.Sleep(1 * time.Second)
		}
		// Subsequent requests (hedges) are fast
		fmt.Fprint(w, "OK")
	}))
	defer ts.Close()

	// 2. Setup Hedging: 100ms delay.
	// If the first request doesn't finish in 100ms, fire the hedge.
	delay := 100 * time.Millisecond
	provider := StaticProvider(delay)

	standardClient := &http.Client{Timeout: 2 * time.Second}
	hedgedClient := WrapDoer(standardClient, provider, 2)

	// 3. Execute the request
	start := time.Now()
	req, _ := http.NewRequest("GET", ts.URL, nil)
	resp, err := hedgedClient.Do(req)

	duration := time.Since(start)

	// 4. Assertions
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	// If hedging works, the duration should be significantly less than 1s.
	// It should be roughly: 100ms (delay) + ~5-10ms (network overhead).
	maxExpected := 300 * time.Millisecond
	if duration > maxExpected {
		t.Errorf("Hedging failed to improve latency. Expected < %v, got %v", maxExpected, duration)
	}

	finalCount := requestCount.Load()
	if finalCount < 2 {
		t.Errorf("Expected at least 2 requests (original + hedge), got %d", finalCount)
	}

	t.Logf("Success! Request finished in %v (instead of 1s) using %d attempts", duration, finalCount)
}

func TestWrapDoer_DynamicAdaptation(t *testing.T) {
	var (
		requestCount atomic.Int32
		// We'll use this to control the server's speed dynamically
		isSlow atomic.Bool
	)

	// 1. Mock Server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count := requestCount.Add(1)

		// If we are in the "Spike" phase and this is the first attempt, be slow.
		// If it's the second attempt (the hedge), be fast.
		if isSlow.Load() && count == 1 {
			time.Sleep(500 * time.Millisecond)
		} else {
			// Training phase or the Hedge attempt
			time.Sleep(10 * time.Millisecond)
		}
		fmt.Fprint(w, "OK")
	}))
	defer ts.Close()

	// 2. Setup Dynamic Provider
	opts := rollingpercentile.Options{
		TotalWindow: time.Minute,
		Percentile:  95.0,
	}
	ctx := context.Background()
	// minDelay of 20ms prevents the hedge from being "too" instant
	provider := NewDynamicProvider(ctx, opts, 20*time.Millisecond)

	standardClient := &http.Client{Timeout: 2 * time.Second}
	hedgedClient := WrapDoer(standardClient, provider, 2)

	// 3. PHASE 1: Training (10 fast requests)
	// This populates the rolling percentile with 10ms data points.
	for i := range 10 {
		req, _ := http.NewRequest("GET", ts.URL, nil)
		resp, err := hedgedClient.Do(req)
		if err != nil {
			t.Fatalf("Training request %d failed: %v", i, err)
		}
		resp.Body.Close()
	}

	// Verify the provider now expects something close to 10-20ms
	currentDelay := provider.GetDelay()
	t.Logf("Trained Delay: %v", currentDelay)

	// 4. PHASE 2: The Latency Spike
	// Enable the "slow path" for the first attempt of the next call
	isSlow.Store(true)
	requestCount.Store(0) // Reset counter for this specific call

	start := time.Now()
	req, _ := http.NewRequest("GET", ts.URL, nil)
	resp, err := hedgedClient.Do(req)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Hedged request failed: %v", err)
	}
	resp.Body.Close()

	t.Logf("Final Request Duration: %v", duration)
	t.Logf("Total attempts made: %d", requestCount.Load())

	// 5. Assertions
	// The total time should be: Hedging Delay (approx 20ms) + Hedge Service Time (10ms)
	// which is ~30-50ms. If it's > 400ms, the hedge didn't fire or finish in time.
	if duration >= 400*time.Millisecond {
		t.Errorf("Dynamic hedging didn't trigger fast enough. Duration: %v", duration)
	}

	if requestCount.Load() < 2 {
		t.Error("Hedge was never fired; only one request reached the server")
	}
}

func TestWrapDoer_PostBodyRepeatability(t *testing.T) {
	var requestCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		body, _ := io.ReadAll(r.Body)
		if string(body) != "payload" {
			t.Errorf("Attempt %d received wrong body: %s", count, string(body))
		}
		if count == 1 {
			time.Sleep(500 * time.Millisecond) // Force a hedge
		}
		fmt.Fprint(w, "OK")
	}))
	defer ts.Close()

	provider := StaticProvider(100 * time.Millisecond)
	client := WrapDoer(http.DefaultClient, provider, 2)

	// Important: We must provide a GetBody function for the request
	// so httphedge can replicate the stream.
	bodyStr := "payload"
	req, _ := http.NewRequest("POST", ts.URL, strings.NewReader(bodyStr))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(bodyStr)), nil
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	if requestCount.Load() < 2 {
		t.Error("Expected hedge to fire for POST request")
	}
}

func TestWrapDoer_ContextCancellation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(1 * time.Second) // Slow enough to be canceled
		fmt.Fprint(w, "OK")
	}))
	defer ts.Close()

	provider := StaticProvider(10 * time.Millisecond)
	client := WrapDoer(http.DefaultClient, provider, 3)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL, nil)
	_, err := client.Do(req)

	if err == nil {
		t.Error("Expected error from canceled context, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "canceled") {
		t.Errorf("Expected context error, got: %v", err)
	}
}

func TestWrapDoer_AllAttemptsFail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Planned Failure", http.StatusInternalServerError)
	}))
	defer ts.Close()

	provider := StaticProvider(10 * time.Millisecond)
	// Even if we try 3 times, if they all fail, we should get an error.
	client := WrapDoer(http.DefaultClient, provider, 3)

	req, _ := http.NewRequest("GET", ts.URL, nil)
	resp, err := client.Do(req)

	// Note: resile.DoHedged behavior depends on whether it treats 500 as a success.
	// In standard RoundTrip, a 500 status is a valid response (err == nil).
	if err != nil {
		t.Fatalf("Did not expect transport error: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Errorf("Expected status 500, got %d", resp.StatusCode)
	}
}
