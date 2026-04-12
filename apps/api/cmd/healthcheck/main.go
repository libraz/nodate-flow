// Command healthcheck is a minimal HTTP probe for distroless containers.
// It exits 0 when GET http://localhost:8080/health returns a 2xx status,
// and non-zero otherwise. Docker HEALTHCHECK invokes this binary directly
// since the distroless image contains no shell, curl, or wget.
package main

import (
	"net/http"
	"os"
	"time"
)

func main() {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://localhost:8080/health")
	if err != nil {
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		os.Exit(0)
	}
	os.Exit(1)
}
