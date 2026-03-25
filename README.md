# httphedge

[httphedge](https://github.com/udhos/httphedge) is a Go library designed to reduce tail latency in HTTP services by implementing Request Hedging.

When a request is slower than expected (e.g., hitting a P95 or P99 latency spike), the package automatically fires a second "hedge" request. The library then returns the result of whichever request finishes first and cancels the other. It supports both Static delays (fixed duration) and Dynamic adaptive delays (using rolling percentiles) to intelligently decide when to hedge based on real-time network conditions.

# Examples

- Static [cmd/httphedge-example-static/main.go](cmd/httphedge-example-static/main.go)

- Dynamic [cmd/httphedge-example-dynamic/main.go](cmd/httphedge-example-dynamic/main.go)

# References

- [Beating Tail Latency: A Guide to Request Hedging in Go Microservices](https://dev.to/onurcinar/beating-tail-latency-a-guide-to-request-hedging-in-go-microservices-p81)

- [resile](https://github.com/cinar/resile)
