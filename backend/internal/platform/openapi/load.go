package openapi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/getkin/kin-openapi/openapi3"
)

// Load reads and validates docs/openapi.yaml from the repo.
func Load(ctx context.Context) (*openapi3.T, error) {
	path, err := SpecPath()
	if err != nil {
		return nil, err
	}
	loader := &openapi3.Loader{Context: ctx, IsExternalRefsAllowed: true}
	doc, err := loader.LoadFromFile(path)
	if err != nil {
		return nil, fmt.Errorf("openapi: load %s: %w", path, err)
	}
	if err := doc.Validate(ctx); err != nil {
		return nil, fmt.Errorf("openapi: validate: %w", err)
	}
	return doc, nil
}

// SpecPath finds docs/openapi.yaml relative to the module or working directory.
func SpecPath() (string, error) {
	candidates := []string{
		"docs/openapi.yaml",
		filepath.Join("..", "docs", "openapi.yaml"),
		filepath.Join("..", "..", "docs", "openapi.yaml"),
		filepath.Join("..", "..", "..", "docs", "openapi.yaml"),
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		// backend/internal/platform/openapi → repo root
		root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
		candidates = append([]string{filepath.Join(root, "docs", "openapi.yaml")}, candidates...)
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "docs", "openapi.yaml"),
			filepath.Join(wd, "..", "docs", "openapi.yaml"),
		)
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("openapi: docs/openapi.yaml not found (tried from %v)", candidates[:min(4, len(candidates))])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
