package output

// AsItem returns the Error as a flat map suitable for per-item JSONL
// emission from bulk commands (e.g. `page info X Y Z`). Pulls Input
// straight from the Error so callers don't re-pass context they already
// handed the constructor. Fields missing on the Error are omitted. Use
// this so per-item failures carry the same recovery hint as single-shot
// errors do on stderr.
func (e *Error) AsItem() map[string]any {
	m := map[string]any{"error": e.Err}
	if e.Input != "" {
		m["input"] = e.Input
	}
	if e.Detail != "" {
		m["detail"] = e.Detail
	}
	if e.Hint != "" {
		m["hint"] = e.Hint
	}
	return m
}
