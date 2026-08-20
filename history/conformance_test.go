package history

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestHistoryV1Fixture(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile("../protocol/history/v1/fixtures/content.json")
	if err != nil {
		t.Fatal(err)
	}
	content = trimFinalNewline(content)
	digest := sha256.Sum256(content)
	if got := hex.EncodeToString(digest[:]); got != "f79545992db9ec4c529e2a5db41a8910f23d1d038778873c201cf2f30f82840e" {
		t.Fatalf("content fixture digest = %s", got)
	}
	recordBytes, err := os.ReadFile("../protocol/history/v1/fixtures/record.json")
	if err != nil {
		t.Fatal(err)
	}
	var record Record
	if err := json.Unmarshal(recordBytes, &record); err != nil {
		t.Fatal(err)
	}
	if err := record.Verify(); err != nil {
		t.Fatal(err)
	}
}

func trimFinalNewline(value []byte) []byte {
	if len(value) > 0 && value[len(value)-1] == '\n' {
		return value[:len(value)-1]
	}
	return value
}
