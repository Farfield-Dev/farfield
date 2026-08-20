package repository

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Farfield-Dev/farfield/internal/buildinfo"
)

func TestPublicJSONFilesAreValid(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, path := range []string{
		"protocol/history/v1/schema.json",
		"protocol/history/v1/fixtures/content.json",
		"protocol/history/v1/fixtures/record.json",
		"protocol/history/v2/schema.json",
		"protocol/runtime/v1/schema.json",
	} {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
}

func TestOpenAPIListsImplementedRoutes(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, route := range []string{
		"/v1/health:",
		"/v1/history/records:",
		"/v1/history/segments:",
		"/v1/history/records/{record_id}:",
		"/v1/history/conversations:",
		"/v1/history/conversations/{conversation_id}/timeline:",
		"/v1/runtime/runs:",
		"/v1/runtime/runs/{run_id}:",
		"/v1/runtime/runs/{run_id}/events:",
		"/v1/runtime/runs/{run_id}/transitions:",
		"/v1/runtime/runs/{run_id}/checkpoints:",
	} {
		if !strings.Contains(text, route) {
			t.Fatalf("openapi.yaml does not contain %s", route)
		}
	}
	if !strings.Contains(text, "version: "+buildinfo.Version) {
		t.Fatalf("OpenAPI version does not match build version %s", buildinfo.Version)
	}
}

func TestRelativeMarkdownLinksResolve(t *testing.T) {
	root := filepath.Join("..", "..")
	pattern := regexp.MustCompile(`\[[^]]+\]\(([^)]+)\)`)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == ".farfield") {
			return filepath.SkipDir
		}
		if entry.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, match := range pattern.FindAllStringSubmatch(string(data), -1) {
			target := strings.Split(match[1], "#")[0]
			if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			if _, statErr := os.Stat(filepath.Join(filepath.Dir(path), filepath.FromSlash(target))); statErr != nil {
				t.Errorf("%s links to missing %s", path, target)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
