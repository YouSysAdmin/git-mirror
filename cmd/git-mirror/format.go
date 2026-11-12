// git-mirror is the CLI/TUI for managing mirrored repositories.
package main

import (
	"fmt"
	"strings"
	"time"
)

func branchesLabel(bs []string) string {
	if len(bs) == 0 {
		return "all"
	}
	return strings.Join(bs, ",")
}

// sshKeyLabel renders an ssh_key value with its kind.
func sshKeyLabel(v string) string {
	switch {
	case v == "":
		return "- (ssh of the service user)"
	case strings.Contains(v, "/"):
		return v + " (path)"
	default:
		return v + " (identity)"
	}
}

func presentLabel(b bool) string {
	if b {
		return "present"
	}
	return "none"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func shortSHA(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	if s == "" {
		return "-"
	}
	return s
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return humanDuration(time.Since(t)) + " ago"
}

func in(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Until(t)
	if d <= 0 {
		return "now"
	}
	return "in " + humanDuration(d)
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%02dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

func humanSize(b int64) string {
	if b <= 0 {
		return "-"
	}
	units := []string{"B", "K", "M", "G", "T"}
	f := float64(b)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d%s", b, units[i])
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", f), ".0") + units[i]
}
