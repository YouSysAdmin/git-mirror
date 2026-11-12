package main

import (
	"net"
	"os"
	"strings"

	"github.com/aymanbagabas/go-osc52/v2"
	tea "github.com/charmbracelet/bubbletea"

	"git-mirror/internal/api"
)

// cloneLinks are the ways a client can clone one mirror.
type cloneLinks struct {
	HTTP          string // http(s) URL without credentials
	HTTPWithToken string // http(s) URL with the token embedded, "" without a token
	SSH           string // ssh URL, "" when the SSH endpoint is disabled
	GitOpts       string // extra git options needed for HTTP (self-signed cert)
	CertURL       string // where the self-signed certificate can be fetched, "" otherwise
	HTTPSelf      bool   // HTTPS with a self-signed certificate
}

// buildCloneLinks renders clone URLs for a repository from the server status
// and the HTTP token. Disabled endpoints leave the corresponding fields empty.
func buildCloneLinks(st api.Status, token, name string) cloneLinks {
	var l cloneLinks
	if host := endpointHost(st, st.Server.GitHTTP, st.Settings.Listen); host != "" {
		scheme := "http"
		if httpsEnabled(st) {
			scheme = "https"
			if st.Settings.TLS.Mode == "self" {
				l.HTTPSelf = true
				l.GitOpts = "-c http.sslCAInfo=mirror.pem"
				l.CertURL = scheme + "://" + host + "/-/cert.pem"
			}
		}
		l.HTTP = scheme + "://" + host + "/" + name + ".git"
		if token != "" {
			l.HTTPWithToken = scheme + "://git:" + token + "@" + host + "/" + name + ".git"
		}
	}
	if host := endpointHost(st, st.Server.GitSSH, st.Settings.SSHListen); host != "" {
		l.SSH = "ssh://git@" + host + "/" + name + ".git"
	}
	return l
}

// withToken embeds a token into the HTTP URL: http://git:<token>@host/name.git
func (l cloneLinks) withToken(token string) string {
	if l.HTTP == "" || token == "" {
		return ""
	}
	return strings.Replace(l.HTTP, "://", "://git:"+token+"@", 1)
}

// gitClone renders a "git clone" command for a URL.
func (l cloneLinks) gitClone(url string) string {
	if l.GitOpts != "" && strings.HasPrefix(url, "http") {
		return "git " + l.GitOpts + " clone " + url
	}
	return "git clone " + url
}

// httpsEnabled reports whether the HTTP endpoint serves TLS (from the running
// server, or from the settings when it is down).
func httpsEnabled(st api.Status) bool {
	if st.Server.Running {
		return st.Server.GitHTTPS
	}
	return st.Settings.TLS.Enabled()
}

// endpointHost returns host:port for clone URLs: the configured public_host
// (or the machine host name) plus the port of the listen address.
func endpointHost(st api.Status, effective, configured string) string {
	addr := effective
	if !st.Server.Running {
		addr = configured
	}
	if addr == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if st.Settings.PublicHost != "" {
		host = st.Settings.PublicHost
	} else if host == "" || host == "0.0.0.0" || host == "::" {
		if hn, err := os.Hostname(); err == nil {
			host = hn
		} else {
			host = "<host>"
		}
	}
	return net.JoinHostPort(host, port)
}

// copyToClipboard puts text into the terminal clipboard with an OSC 52
// sequence. Supported by alacritty, kitty, foot, ghostty, wezterm, iTerm2,
// Windows Terminal and tmux. Inside tmux the plain sequence is sent: tmux
// stores it in its paste buffer and forwards it to the outer terminal when
// set-clipboard is on or external (the default). GNU screen needs the DCS
// passthrough wrapper. Terminals without support ignore the sequence.
func copyToClipboard(text string) error {
	seq := osc52.New(text)
	if os.Getenv("TMUX") == "" && strings.HasPrefix(os.Getenv("TERM"), "screen") {
		seq = seq.Screen()
	}
	_, err := seq.WriteTo(os.Stdout)
	return err
}

// copyCmd copies text and reports the outcome as a notice.
func copyCmd(label, text string) tea.Cmd {
	return func() tea.Msg {
		if err := copyToClipboard(text); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{text: "copied " + label + " to clipboard"}
	}
}
