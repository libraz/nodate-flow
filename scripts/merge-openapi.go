// Command merge-openapi merges multiple OpenAPI 3.1 JSON files into one.
// It combines paths and component schemas, with earlier files taking
// precedence on collisions.
//
// Usage:
//
//	go run merge-openapi.go -o merged.json flow.json auth.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	out := flag.String("o", "openapi.json", "output path")
	flag.Parse()
	files := flag.Args()
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "merge-openapi: no input files")
		os.Exit(1)
	}

	var root map[string]any
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "merge-openapi: read %s: %v\n", f, err)
			os.Exit(1)
		}
		var spec map[string]any
		if err := json.Unmarshal(data, &spec); err != nil {
			fmt.Fprintf(os.Stderr, "merge-openapi: parse %s: %v\n", f, err)
			os.Exit(1)
		}
		if root == nil {
			root = spec
			continue
		}
		mergePaths(root, spec)
		mergeSchemas(root, spec)
	}

	buf, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "merge-openapi: marshal: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, append(buf, '\n'), 0o644); err != nil { //nolint:gosec // codegen artifact
		fmt.Fprintf(os.Stderr, "merge-openapi: write: %v\n", err)
		os.Exit(1)
	}

	pathCount := 0
	if p, ok := root["paths"].(map[string]any); ok {
		pathCount = len(p)
	}
	fmt.Printf("merge-openapi: wrote %s (%d paths)\n", *out, pathCount)
}

func mergePaths(dst, src map[string]any) {
	srcPaths, ok := src["paths"].(map[string]any)
	if !ok {
		return
	}
	dstPaths, ok := dst["paths"].(map[string]any)
	if !ok {
		dstPaths = map[string]any{}
		dst["paths"] = dstPaths
	}
	for path, item := range srcPaths {
		if _, exists := dstPaths[path]; !exists {
			dstPaths[path] = item
		} else {
			// Merge HTTP methods within the same path.
			dstItem, _ := dstPaths[path].(map[string]any)
			srcItem, _ := item.(map[string]any)
			if dstItem != nil && srcItem != nil {
				for method, op := range srcItem {
					if _, exists := dstItem[method]; !exists {
						dstItem[method] = op
					}
				}
			}
		}
	}
}

func mergeSchemas(dst, src map[string]any) {
	srcComp, ok := src["components"].(map[string]any)
	if !ok {
		return
	}
	dstComp, ok := dst["components"].(map[string]any)
	if !ok {
		dstComp = map[string]any{}
		dst["components"] = dstComp
	}
	srcSchemas, ok := srcComp["schemas"].(map[string]any)
	if !ok {
		return
	}
	dstSchemas, ok := dstComp["schemas"].(map[string]any)
	if !ok {
		dstSchemas = map[string]any{}
		dstComp["schemas"] = dstSchemas
	}
	for name, schema := range srcSchemas {
		if _, exists := dstSchemas[name]; !exists {
			dstSchemas[name] = schema
		}
	}
}
