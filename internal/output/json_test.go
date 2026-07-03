package output

import (
	"testing"
)

func TestEnrichTimestamps_QualifyingKeys(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		wantISO string // "" means no _iso added
	}{
		{"created_at ISO8601", "created_at", "2024-01-15T09:50:00Z", "2024-01-15T09:50:00Z"},
		{"modified_at ISO8601", "modified_at", "2024-01-15T09:50:00.123Z", "2024-01-15T09:50:00Z"},
		{"created_at with offset normalized to UTC", "created_at", "2024-01-15T04:50:00-05:00", "2024-01-15T09:50:00Z"},
		{"suffix _ts ISO8601", "updated_ts", "2024-01-15T09:50:00Z", "2024-01-15T09:50:00Z"},
		{"exact ts epoch fallback", "ts", "1705312200.123456", "2024-01-15T09:50:00Z"},
		{"created_at epoch fallback", "created_at", "1705312200.000000", "2024-01-15T09:50:00Z"},
		{"non-timestamp key ignored", "events", "2024-01-15T09:50:00Z", ""},
		{"unparseable value adds nothing", "created_at", "not-a-date", ""},
		{"epoch out of bounds adds nothing", "ts", "100.5", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := map[string]any{tt.key: tt.value}
			enrichTimestamps(m)
			iso, ok := m[tt.key+"_iso"]
			if tt.wantISO == "" {
				if ok {
					t.Errorf("expected no %s_iso, got %v", tt.key, iso)
				}
				return
			}
			if !ok {
				t.Fatalf("expected %s_iso to be added", tt.key)
			}
			if iso != tt.wantISO {
				t.Errorf("got %s_iso=%q, want %q", tt.key, iso, tt.wantISO)
			}
		})
	}
}

func TestEnrichTimestamps_Nested(t *testing.T) {
	m := map[string]any{
		"ts": "1705312200.123456",
		"version": map[string]any{
			"created_at": "2024-01-15T09:50:00Z",
		},
		"history": []any{
			map[string]any{"modified_at": "2024-01-15T09:50:00Z"},
		},
	}
	enrichTimestamps(m)

	if _, ok := m["ts_iso"]; !ok {
		t.Error("missing top-level ts_iso")
	}
	nested, ok := m["version"].(map[string]any)
	if !ok {
		t.Fatal("version is not a map")
	}
	if _, ok := nested["created_at_iso"]; !ok {
		t.Error("missing nested created_at_iso")
	}
	arr, ok := m["history"].([]any)
	if !ok {
		t.Fatal("history is not a slice")
	}
	item, ok := arr[0].(map[string]any)
	if !ok {
		t.Fatal("history[0] is not a map")
	}
	if _, ok := item["modified_at_iso"]; !ok {
		t.Error("missing modified_at_iso in slice element")
	}
}

func TestEnrichTimestamps_DoesNotOverwriteExistingISO(t *testing.T) {
	m := map[string]any{
		"created_at":     "2024-01-15T09:50:00Z",
		"created_at_iso": "PRESET",
	}
	enrichTimestamps(m)
	if m["created_at_iso"] != "PRESET" {
		t.Errorf("existing created_at_iso overwritten: got %v", m["created_at_iso"])
	}
}

func TestEnrichTimestamps_NonStringValueIgnored(t *testing.T) {
	m := map[string]any{"ts": 12345}
	enrichTimestamps(m)
	if _, ok := m["ts_iso"]; ok {
		t.Error("non-string ts should not gain ts_iso")
	}
}
