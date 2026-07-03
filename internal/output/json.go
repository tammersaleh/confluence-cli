package output

import (
	"strconv"
	"strings"
	"time"
)

// enrichTimestamps walks a JSON map and adds <key>_iso fields next to
// timestamp fields. A field qualifies if its key is exactly "ts", ends in
// "_ts", or is exactly "created_at" or "modified_at". The value is normalized
// to RFC3339 in UTC. Confluence returns ISO 8601 strings; a Slack-style
// "epoch.microseconds" value is also accepted as a fallback.
func enrichTimestamps(v any) {
	switch val := v.(type) {
	case map[string]any:
		enrichMap(val)
	case []any:
		for _, item := range val {
			enrichTimestamps(item)
		}
	}
}

func enrichMap(m map[string]any) {
	additions := map[string]string{}
	for k, v := range m {
		if isTimestampKey(k) {
			if _, exists := m[k+"_iso"]; !exists {
				if s, ok := v.(string); ok {
					if iso, ok := tsToISO(s); ok {
						additions[k+"_iso"] = iso
					}
				}
			}
		}
		enrichTimestamps(v)
	}
	for k, v := range additions {
		m[k] = v
	}
}

func isTimestampKey(key string) bool {
	return key == "ts" || strings.HasSuffix(key, "_ts") ||
		key == "created_at" || key == "modified_at"
}

// tsToISO normalizes a timestamp string to RFC3339 in UTC. It tries ISO 8601
// (RFC3339 / RFC3339Nano) first, then falls back to the Slack-style
// "epoch.microseconds" form. Returns ok=false if neither parses.
func tsToISO(ts string) (string, bool) {
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return t.UTC().Format(time.RFC3339), true
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.UTC().Format(time.RFC3339), true
	}
	return slackTsToISO(ts)
}

func slackTsToISO(ts string) (string, bool) {
	parts := strings.SplitN(ts, ".", 2)
	if len(parts) != 2 {
		return "", false
	}

	sec, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return "", false
	}

	if sec < 946684800 || sec > 4102444800 {
		return "", false
	}

	if _, err := strconv.ParseInt(parts[1], 10, 64); err != nil {
		return "", false
	}

	t := time.Unix(sec, 0).UTC()
	return t.Format(time.RFC3339), true
}
