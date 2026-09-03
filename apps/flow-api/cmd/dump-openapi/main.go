// Command dump-openapi builds the nodate-flow HTTP router with stub
// dependencies and writes the merged OpenAPI 3.1 document to disk so
// the TypeScript SDK codegen pipeline can consume it without running a
// live server. The router registers operations at build time, so no
// database or cipher is required.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/router"
)

func main() {
	out := flag.String("o", "packages/sdk/openapi.json", "output path for the merged OpenAPI JSON document")
	flag.Parse()

	// Stub JWT issuer: dump-openapi never signs anything, it just needs a
	// non-nil value so middleware.RequireAuth wires up cleanly at build
	// time.
	issuer, err := auth.NewJWTIssuer(nil, "nodate-flow", "api", 15*time.Minute)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dump-openapi: jwt issuer: %v\n", err)
		os.Exit(1)
	}

	res := router.BuildResult(router.Deps{JWT: issuer})
	merged, err := router.MergeAPIs(res.APIs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dump-openapi: %v\n", err)
		os.Exit(1)
	}
	buf, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "dump-openapi: marshal: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, append(buf, '\n'), 0o644); err != nil { //nolint:gosec // codegen artifact
		fmt.Fprintf(os.Stderr, "dump-openapi: write %s: %v\n", *out, err)
		os.Exit(1)
	}
	fmt.Printf("dump-openapi: wrote %s (%d paths)\n", *out, len(merged.Paths))
}
