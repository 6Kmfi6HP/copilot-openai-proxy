package copilot

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// truncateForLog bounds an upstream body before it is embedded in an error or log
// line so a large or hostile response cannot flood the logs.
func truncateForLog(s string) string {
	const max = 256
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
