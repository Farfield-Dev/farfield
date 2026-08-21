package history

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/Farfield-Dev/farfield/internal/canonicaljson"
)

func TestHistoryV2Fixture(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../protocol/history/v2/fixtures/segment.json")
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.TrimSuffix(data, []byte("\n"))
	var segment Segment
	if err := json.Unmarshal(data, &segment); err != nil {
		t.Fatal(err)
	}
	if err := segment.Verify(); err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicaljson.Marshal(segment)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, canonical) {
		t.Fatal("History v2 fixture is not canonical JSON")
	}
}
