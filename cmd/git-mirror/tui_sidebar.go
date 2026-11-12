package main

import (
	"fmt"
	"time"

	"git-mirror/internal/config"
)

// sidebarKeysMinHeight is the terminal height from which the KEYS section fits.
const sidebarKeysMinHeight = 48

func (m tuiModel) viewSidebar() string {
	var b sb
	b.line(titleStyle.Render(logo), "\n")

	inner := sidebarWidth - 4
	kv := func(k, v string) {
		b.line(detailLabelStyle.Width(10).Render(k), " ", truncate(v, inner-11))
	}

	b.line(sectionStyle.Render("SERVER"))
	if m.status.Server.Running {
		kv("status", okStyle.Render("● running"))
		kv("pid", fmt.Sprint(m.status.Server.PID))
		kv("uptime", humanDuration(time.Since(m.status.Server.Started)))
		kv("queued", fmt.Sprint(m.status.Server.Queued))
	} else if m.loaded {
		kv("status", warnStyle.Render("○ not running"))
		kv("", mutedStyle.Render("sync runs locally"))
	} else {
		kv("status", mutedStyle.Render(m.spinner.View()+" connecting"))
	}
	b.line("\n", sectionStyle.Render("SETTINGS"))
	kv("interval", m.status.Settings.SyncInterval.String())
	kv("timeout", m.status.Settings.SyncTimeout.String())
	kv("workers", fmt.Sprint(m.status.Settings.Workers))
	kv("ssh keys", fmt.Sprint(len(m.keys)))
	kv("tokens", fmt.Sprint(len(m.tokens)))
	kv("identities", fmt.Sprint(len(m.identities)))
	if m.status.Settings.Proxy != "" {
		kv("proxy", m.status.Settings.Proxy)
	}
	if m.app.HasSSHConfig() {
		kv("ssh_config", "present")
	}
	// Endpoints: the running server reports the effective addresses, which
	// may come from command line flags rather than config.json.
	b.line("\n", sectionStyle.Render("ENDPOINTS"))
	kv("http", m.endpointLabel(m.status.Server.GitHTTP, m.status.Settings.Listen))
	if httpsEnabled(m.status) {
		kv("tls", m.status.Settings.TLS.Mode)
	}
	kv("ssh", m.endpointLabel(m.status.Server.GitSSH, m.status.Settings.SSHListen))
	kv("data dir", truncateLeft(m.app.Dir.String(), inner-11))

	var ok, failed, syncing, never, disabled int
	for _, r := range m.status.Repos {
		if !r.Repo.Enabled {
			disabled++
		}
		switch {
		case r.InFlight || r.State.Status == config.StatusSyncing:
			syncing++
		case r.State.Status == config.StatusOK:
			ok++
		case r.State.Status == config.StatusError:
			failed++
		default:
			never++
		}
	}
	b.line("\n", sectionStyle.Render("REPOSITORIES"))
	kv("total", fmt.Sprint(len(m.status.Repos)))
	kv("ok", okStyle.Render(fmt.Sprint(ok)))
	if failed > 0 {
		kv("failed", errorTextStyle.Render(fmt.Sprint(failed)))
	} else {
		kv("failed", "0")
	}
	kv("syncing", fmt.Sprint(syncing))
	kv("never", fmt.Sprint(never))
	kv("disabled", fmt.Sprint(disabled))

	if m.height < sidebarKeysMinHeight {
		return b.String() // the content pane shows the key hints anyway
	}
	b.line("\n", sectionStyle.Render("KEYS"))
	for _, h := range [][2]string{
		{"a", "add repository"},
		{"d", "remove"},
		{"s / enter", "sync selected"},
		{"S", "sync all"},
		{"e", "edit interval / branches / key"},
		{"t", "enable / disable"},
		{"c", "clone links"},
		{"T", "http access tokens"},
		{"K / tab", "ssh client keys"},
		{"I", "ssh identities"},
		{"/", "filter"},
		{"r", "refresh"},
		{"?", "help"},
		{"q", "quit"},
	} {
		b.line(hintKeyStyle.Width(10).Render(h[0]), " ", hintStyle.Render(h[1]))
	}
	return b.String()
}

// where it comes from when the running server disagrees with config.json.
func (m tuiModel) endpointLabel(effective, configured string) string {
	if !m.status.Server.Running {
		if configured == "" {
			return mutedStyle.Render("off")
		}
		return configured + mutedStyle.Render(" (config)")
	}
	switch {
	case effective == "" && configured == "":
		return mutedStyle.Render("off")
	case effective == "":
		return mutedStyle.Render("off (flag)")
	case configured == "":
		return effective + mutedStyle.Render(" (flag)")
	case effective != configured:
		return effective + mutedStyle.Render(" (flag)")
	default:
		return effective
	}
}
