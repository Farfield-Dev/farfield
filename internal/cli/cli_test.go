package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestHistoryCLIWorkflow(t *testing.T) {
	t.Parallel()
	store := filepath.Join(t.TempDir(), "objects")
	searchCache := filepath.Join(t.TempDir(), "search.json.gz")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"history", "append", "--store", store, "--id", "rec_cli",
		"--conversation", "conv_cli", "--kind", "model.response", "--content", `{"text":"hello"}`,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("append exit = %d, stderr = %s", code, stderr.String())
	}
	var appended map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &appended); err != nil {
		t.Fatal(err)
	}
	if appended["id"] != "rec_cli" {
		t.Fatalf("append output = %#v", appended)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"history", "projections", "rebuild", "--store", store}, &stdout, &stderr)
	if code != 0 || !bytes.Contains(stdout.Bytes(), []byte(`"projection": "conversations"`)) || !bytes.Contains(stdout.Bytes(), []byte(`"source_count": 1`)) {
		t.Fatalf("projection rebuild exit = %d, stdout = %s, stderr = %s", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"history", "timeline", "--store", store, "--conversation", "conv_cli"}, &stdout, &stderr)
	if code != 0 || !bytes.Contains(stdout.Bytes(), []byte(`"text": "hello"`)) {
		t.Fatalf("timeline exit = %d, stdout = %s, stderr = %s", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"history", "search", "--store", store, "--cache", searchCache, "--text", "hello", "--conversation", "conv_cli"}, &stdout, &stderr)
	if code != 0 || !bytes.Contains(stdout.Bytes(), []byte(`"total": 1`)) || !bytes.Contains(stdout.Bytes(), []byte(`"rec_cli"`)) {
		t.Fatalf("search exit = %d, stdout = %s, stderr = %s", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"history", "search-index", "rebuild", "--store", store, "--cache", searchCache}, &stdout, &stderr)
	if code != 0 || !bytes.Contains(stdout.Bytes(), []byte(`"indexed_records": 1`)) {
		t.Fatalf("search rebuild exit = %d, stdout = %s, stderr = %s", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"history", "verify", "--store", store}, &stdout, &stderr)
	if code != 0 || !bytes.Contains(stdout.Bytes(), []byte(`"ok": true`)) {
		t.Fatalf("verify exit = %d, stdout = %s, stderr = %s", code, stdout.String(), stderr.String())
	}
}

func TestHistoryAppendBatchCLI(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store := filepath.Join(root, "objects")
	batch := filepath.Join(root, "batch.json")
	data := []byte(`{"id":"seg_cli","records":[{"id":"rec_cli_one","conversation_id":"conv_cli","kind":"message.input","content":{"text":"hello"}},{"id":"rec_cli_two","conversation_id":"conv_cli","kind":"message.output","content":{"text":"hi"}}]}`)
	if err := os.WriteFile(batch, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run([]string{"history", "append-batch", "--store", store, "--file", batch}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("append-batch exit = %d, stderr = %s", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"id": "seg_cli"`)) {
		t.Fatalf("append-batch output = %s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"history", "timeline", "--store", store, "--conversation", "conv_cli"}, &stdout, &stderr)
	if code != 0 || !bytes.Contains(stdout.Bytes(), []byte(`"rec_cli_two"`)) {
		t.Fatalf("timeline exit = %d, stdout = %s, stderr = %s", code, stdout.String(), stderr.String())
	}
}
