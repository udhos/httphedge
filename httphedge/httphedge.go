// Package httphedge implements request hedging for http.
package httphedge

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/cinar/resile"
	"github.com/udhos/rollingpercentile/rollingpercentile"
)

// DelayProvider defines the strategy for calculating the hedging delay.
type DelayProvider interface {
	GetDelay() time.Duration
	Record(latency time.Duration)
}

// StaticProvider uses a fixed duration for every request.
type StaticProvider time.Duration

// GetDelay returns the provider delay.
func (s StaticProvider) GetDelay() time.Duration { return time.Duration(s) }

// Record records request latency.
func (s StaticProvider) Record(_ time.Duration) {} // No-op

// DynamicProvider uses rolling percentiles to adapt to network conditions.
type DynamicProvider struct {
	tracker  *rollingpercentile.RollingPercentile
	minDelay time.Duration
}

// NewDynamicProvider creates a DynamicProvider.
func NewDynamicProvider(ctx context.Context, opts rollingpercentile.Options,
	minDelay time.Duration) *DynamicProvider {
	return &DynamicProvider{
		tracker:  rollingpercentile.NewRollingPercentile(ctx, opts),
		minDelay: minDelay,
	}
}

// GetDelay returns the provider delay.
func (d *DynamicProvider) GetDelay() time.Duration {
	p := d.tracker.Get()
	delay := time.Duration(p) * time.Millisecond
	if delay < d.minDelay {
		return d.minDelay
	}
	return delay
}

// Record records request latency.
func (d *DynamicProvider) Record(latency time.Duration) {
	d.tracker.Record(int64(latency.Milliseconds()))
}

// Transport implements http.RoundTripper with hedging logic.
type Transport struct {
	// Base is the underlying round tripper (defaults to http.DefaultTransport).
	Base http.RoundTripper
	// Provider determines the hedging delay (Static or Dynamic).
	Provider DelayProvider
	// MaxAttempts is the total number of allowed requests (e.g., 2 means 1 original + 1 hedge).
	MaxAttempts uint
}

// RoundTrip implements RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}

	delay := t.Provider.GetDelay()

	// The action to be executed by Resile
	action := func(ctx context.Context) (any, error) {
		// 1. Clone the request for thread-safety across concurrent attempts
		clonedReq := req.Clone(ctx)

		// 2. Handle Body repeatability. http.Request.GetBody allows us to
		// create a fresh stream for each hedged attempt (important for POST/PUT).
		if req.Body != nil && req.GetBody != nil {
			var err error
			clonedReq.Body, err = req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("httphedge: failed to get body: %w", err)
			}
		}

		return base.RoundTrip(clonedReq)
	}

	start := time.Now()

	// 3. Execute with Resile's Hedging engine
	result, err := resile.DoHedged(req.Context(), action,
		resile.WithMaxAttempts(t.MaxAttempts),
		resile.WithHedgingDelay(delay),
	)

	if err == nil {
		// Success! Record latency for the provider.
		t.Provider.Record(time.Since(start))
		return result.(*http.Response), nil
	}

	return nil, err
}

// HTTPDoer is abstract interface for http.Client.Do.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// hedgedDoer wraps an existing HTTPDoer and attaches the hedging Transport logic.
type hedgedDoer struct {
	client    HTTPDoer
	transport *Transport
}

// Do implements the HTTPDoer interface.
func (h *hedgedDoer) Do(req *http.Request) (*http.Response, error) {
	// We reuse the RoundTrip logic from your Transport struct
	// because it already handles the Resile hedging engine.
	return h.transport.RoundTrip(req)
}

// WrapDoer wraps any HTTPDoer (like an http.Client) with hedging logic.
func WrapDoer(client HTTPDoer, provider DelayProvider, maxAttempts int) HTTPDoer {
	// If the client is already an *http.Client, we can extract its transport.
	// Otherwise, we treat the 'client' itself as the Base round tripper.
	var base http.RoundTripper

	if c, ok := client.(*http.Client); ok && c.Transport != nil {
		base = c.Transport
	} else {
		// Fallback: Create a bridge so the Transport can call the original Doer
		base = &doerToTransportBridge{client}
	}

	return &hedgedDoer{
		client: client,
		transport: &Transport{
			Base:        base,
			Provider:    provider,
			MaxAttempts: uint(maxAttempts),
		},
	}
}

// Internal bridge to allow the Transport.RoundTrip to call a generic HTTPDoer
type doerToTransportBridge struct {
	doer HTTPDoer
}

// RoundTrip implements RoundTripper.
func (b *doerToTransportBridge) RoundTrip(req *http.Request) (*http.Response, error) {
	return b.doer.Do(req)
}
