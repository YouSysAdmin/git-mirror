package mirror

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestSSHCommand(t *testing.T) {
	if (Options{}).SSHCommand() != "" {
		t.Fatal("no ssh options must leave GIT_SSH_COMMAND unset")
	}
	o := Options{SSHKey: "/d/identities/deploy", KnownHosts: "/d/known_hosts", ConfigFile: "/d/ssh config",
		ProxyCommand: "'/opt/git mirror/git-mirror' ssh-proxy 'http://p:8080/?a=1&b=2' %h %p"}
	cmd := o.SSHCommand()
	for _, want := range []string{"-F '/d/ssh config'", "-i '/d/identities/deploy' -o IdentitiesOnly=yes",
		"-o 'UserKnownHostsFile=/d/known_hosts'", "-o BatchMode=yes -o StrictHostKeyChecking=accept-new"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("missing %q in %q", want, cmd)
		}
	}
	// git hands GIT_SSH_COMMAND to /bin/sh, so the quoting must survive one
	// round of shell parsing and yield the ProxyCommand value verbatim.
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not installed")
	}
	out, err := exec.Command("sh", "-c", `printf '%s\n' `+strings.TrimPrefix(cmd, "ssh ")).Output()
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if !slices.Contains(args, "ProxyCommand="+o.ProxyCommand) {
		t.Fatalf("proxy command not preserved: %q", args)
	}
	if !slices.Contains(args, "/d/ssh config") {
		t.Fatalf("config path not preserved: %q", args)
	}
}

func TestEnvHTTPProxy(t *testing.T) {
	env := Options{}.env()
	if slices.ContainsFunc(env, func(s string) bool { return strings.HasPrefix(s, "GIT_CONFIG_COUNT=") }) {
		t.Fatal("no proxy must not set GIT_CONFIG_*")
	}
	env = Options{HTTPProxy: "http://u:p@proxy:8080", HTTPProxyInsecure: true}.env()
	for _, want := range []string{"GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=http.proxy", "GIT_CONFIG_VALUE_0=http://u:p@proxy:8080",
		"GIT_CONFIG_KEY_1=http.proxySSLVerify", "GIT_CONFIG_VALUE_1=false"} {
		if !slices.Contains(env, want) {
			t.Errorf("missing %q", want)
		}
	}
}
