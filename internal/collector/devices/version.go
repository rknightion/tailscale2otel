package devices

import "regexp"

// versionRe captures the leading MAJOR.MINOR.PATCH of a Tailscale client version
// string, tolerating a leading "v" and any suffix (-t<hash>, -dev<date>, -dirty,
// " (OpenWrt)", "-1", etc.). Tailscale versions are always three-component.
var versionRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

// NormalizeVersion reduces a raw client-version string to its MAJOR.MINOR.PATCH
// prefix (e.g. "1.98.4-t01c6b9661" -> "1.98.4"), bounding by_version cardinality.
// Empty or unparseable input returns "unknown".
func NormalizeVersion(raw string) string {
	m := versionRe.FindStringSubmatch(raw)
	if m == nil {
		return "unknown"
	}
	return m[1] + "." + m[2] + "." + m[3]
}
