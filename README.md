# git-mirror

A small self-hosted service that keeps copies of remote git repositories (GitHub, GitLab or
any git URL) inside your own network and lets people and CI clone them from there.

Typical reasons to run it:

- CI runners or build hosts have no internet access, but need `git clone`
- you want to keep working when GitHub is down or rate-limits you
- you want a local, always fresh copy of the upstream projects you depend on

It is a single binary with no database. You register repositories with the CLI or the TUI,
the server fetches them on a schedule and serves them read-only over HTTP(S) and SSH.

```
 GitHub / GitLab / any git URL
            |
            |  git fetch every 15m (https, ssh, through a proxy if needed)
            v
  +-------------------+        git clone http(s)://mirror/<name>.git   (token)
  |  git-mirror       | <----  git clone ssh://git@mirror:2222/<name>.git (key)
  |  server           |
  +-------------------+
            ^
            |  data dir: config.json, state.json, repos/
            |
  git-mirror add / ls / sync / tui
```

## Contents

- [Install](#install)
- [Quick start](#quick-start)
- [Configuration examples](#configuration-examples)
    - [Plain HTTP in a trusted network](#plain-http-in-a-trusted-network)
    - [HTTPS with a self-signed certificate](#https-with-a-self-signed-certificate)
    - [HTTPS with your own certificate](#https-with-your-own-certificate)
    - [HTTPS with Let's Encrypt (ACME)](#https-with-lets-encrypt-acme)
    - [SSH only](#ssh-only)
    - [Private upstreams over SSH](#private-upstreams-over-ssh)
    - [Private upstreams over HTTPS](#private-upstreams-over-https)
    - [Behind a corporate proxy](#behind-a-corporate-proxy)
    - [Per-team access](#per-team-access)
    - [Mirroring only some branches](#mirroring-only-some-branches)
    - [config.json](#configjson)
- [Settings reference](#settings-reference)
- [Cloning from the mirror](#cloning-from-the-mirror)
- [How syncing works](#how-syncing-works)
- [Mirror names](#mirror-names)
- [TUI](#tui)
- [Command reference](#command-reference)
- [Data dir](#data-dir)
- [Control API](#control-api)
- [Development](#development)

## Install

Every GitHub release ships archives for Linux (amd64, arm64, armv7) and macOS (amd64, arm64),
`deb`/`rpm`/`apk` packages and a container image. `git` must be in `PATH` (the packages and the
image already depend on it).

**Package** (creates the `git` user with home `/home/git`, its data dir and a systemd unit):

```sh
sudo dpkg -i git-mirror_linux_amd64.deb      # or rpm -i / apk add --allow-untrusted
sudo systemctl enable --now git-mirror
```

The unit runs `git-mirror init` and then `git-mirror server` as user `git`. The data dir is
the default one of that user, `/home/git/.local/share/git-mirror`, so no `GIT_MIRROR_DIR` is
needed. On the first start `init` writes an initial `config.json` there with HTTP on `:8080`
and SSH on `:2222`. Ports are not set in the unit, change them with `git-mirror config set`.
Run the CLI as the same user and it finds the same config:

```sh
sudo -u git git-mirror add https://github.com/golang/go.git
sudo -u git git-mirror tui
sudo -u git git-mirror path        # /home/git/.local/share/git-mirror
```

Upgrading from a version whose unit passed `--listen` and `--ssh-listen`: an existing
`config.json` is kept, so set the ports once before restarting:

```sh
sudo -u git git-mirror config set listen :8080
sudo -u git git-mirror config set ssh_listen :2222
```

**Binary with systemd** (without the package):

```sh
sudo useradd -r -m -d /home/git -s /usr/sbin/nologin git
sudo install -m 0755 git-mirror /usr/bin/git-mirror
sudo install -m 0644 deploy/git-mirror.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now git-mirror
```

The unit sandboxes the server: `ProtectHome=read-only` makes `/home` read-only and
`ReadWritePaths` opens only `/home/git/.local/share/git-mirror` for writing (created by
`git-mirror init` in `ExecStartPre`). If the home of `git` is somewhere else, change both paths in the
unit. If you set `GIT_MIRROR_DIR` in the unit, put the same path into `ReadWritePaths` and pass
it to the CLI too (`-d <dir>`), otherwise the server fails with `read-only file system`.

**Container** (data in `/data`, HTTP on 8080, SSH on 2222):

```sh
docker run -d --name git-mirror \
  -v git-mirror:/data -p 8080:8080 -p 2222:2222 \
  ghcr.io/yousysadmin/git-mirror

docker exec git-mirror git-mirror add https://github.com/golang/go.git
docker exec -it git-mirror git-mirror tui
```

On the first start the server writes the initial `config.json` into `/data`. Ports come from
it, the same as with systemd, so change them with `docker exec git-mirror git-mirror config set`
and `docker restart git-mirror` (and adjust the `-p` mappings).

**From source** (Go 1.27+):

```sh
task build              # or make build -> bin/git-mirror
go install ./cmd/git-mirror
```

Once `git-mirror` is in `PATH` it also works as a git subcommand: `git mirror ls`.

## Quick start

A local try-out, no root needed:

```sh
export GIT_MIRROR_DIR=$HOME/git-mirror-data

# 1. tell it what to mirror
git-mirror add https://github.com/golang/go.git          # mirror name: "go"
git-mirror add https://github.com/spf13/cobra.git --interval 1h

# 2. start the server (keep it running in another terminal, tmux or systemd)
git-mirror server            # HTTP on :8080, SSH on :2222 from the initial config

# 3. see what is going on
git-mirror ls -l
git-mirror status
git-mirror tui
```

On the first start the server generates an access token for HTTP. Clone from the mirror:

```sh
git clone http://git:$(git-mirror token)@localhost:8080/go.git

git-mirror keys add ~/.ssh/id_ed25519.pub
git clone ssh://git@localhost:2222/go.git
```

`git-mirror url go --command` prints a ready-to-paste clone command. In the TUI press `c` on a
repository to see its clone URLs.

## Configuration examples

All settings are changed with `git-mirror config set <key> <value>` (or in the TUI) and are
stored in `config.json` in the data dir. `git-mirror config` shows the current values.

The file is created by `git-mirror init`, by the first server start or by the first CLI command
that changes something. A new config has HTTP on `:8080` and SSH on `:2222`, everything else
uses the defaults from the [settings reference](#settings-reference).

Most settings are picked up by the running server immediately. `listen`, `ssh_listen`,
`workers` and every `tls_*` key need a **server restart**.

> The `--listen` and `--ssh-listen` flags of `git-mirror server` override the `listen` and
> `ssh_listen` settings. The systemd unit and the container image pass no flags, so the
> settings apply. `--listen -` turns HTTP off, `--ssh-listen -` turns SSH off.

### Plain HTTP in a trusted network

The default. Good enough for an isolated build network.

```sh
git-mirror config set listen :8080
git-mirror config set public_host mirror.internal   # name used in the clone URLs
```

```sh
git clone http://git:<token>@mirror.internal:8080/go.git
```

The token travels in clear text, so use HTTPS as soon as the network is not fully trusted.

### HTTPS with a self-signed certificate

No CA needed. git-mirror creates a certificate for `public_host` (or `tls_fqdn`) and keeps it
in `<data-dir>/certs`.

```sh
git-mirror config set public_host mirror.internal
git-mirror config set listen :8443
git-mirror config set tls_mode self
# optional
git-mirror config set tls_fqdn mirror.internal      # certificate name, default: public_host
git-mirror config set tls_alg ecdsa                 # ecdsa (default), rsa or ed25519
# restart the server
```

Clients have to trust that certificate. It is published by the server without a token:

```sh
curl -sk https://mirror.internal:8443/-/cert.pem > mirror.pem   # or on the server: git-mirror cert
git-mirror cert --fingerprint                                   # compare with what you downloaded

# trust it for this host only
git config --global http.https://mirror.internal:8443/.sslCAInfo "$PWD/mirror.pem"
git clone https://git:<token>@mirror.internal:8443/go.git
```

### HTTPS with your own certificate

Use this when you have a corporate CA or already get certificates some other way (certbot,
cert-manager, Vault, ...).

```sh
git-mirror config set listen :8443
git-mirror config set tls_mode manual
git-mirror config set tls_cert /etc/git-mirror/tls/fullchain.pem
git-mirror config set tls_key  /etc/git-mirror/tls/privkey.pem
git-mirror config set tls_watch true    # reload the files when they are renewed, no restart
# restart the server
```

The files must be readable by the user that runs the server (`git` in the package). Put the
full chain into `tls_cert` so clients can verify it without extra setup.

### HTTPS with Let's Encrypt (ACME)

For a server that is reachable from the internet under a real DNS name. Certificates are
issued and renewed automatically and cached in `<data-dir>/certs/acme`.

Requirements:

- the DNS name points to the server
- the HTTPS endpoint is reachable from the internet on port **443**
- the HTTP-01 challenge listener is reachable on port **80** (it also redirects plain HTTP to
  HTTPS)

```sh
git-mirror config set public_host mirror.example.com
git-mirror config set listen :443
git-mirror config set tls_mode acme
git-mirror config set tls_acme_hosts mirror.example.com    # comma separated list, at least one
git-mirror config set tls_acme_email ops@example.com       # optional, for expiry notices
git-mirror config set tls_acme_http :80                    # challenge listener, default :80
```

```sh
git clone https://git:<token>@mirror.example.com/go.git
```

**With the systemd unit.** The ports come from the settings above, but the service user cannot
bind ports below 1024. Allow it with a drop-in (`sudo systemctl edit git-mirror`):

```ini
[Service]
AmbientCapabilities=CAP_NET_BIND_SERVICE
```

Then `sudo systemctl restart git-mirror` and watch `journalctl -u git-mirror` for the
`ACME HTTP challenge listener` line.

**With the container.** Keep unprivileged ports inside the container and map them:

```sh
docker run -d --name git-mirror --restart unless-stopped \
  -v git-mirror:/data \
  -p 443:8443 -p 80:8081 -p 2222:2222 \
  ghcr.io/yousysadmin/git-mirror

docker exec git-mirror git-mirror config set public_host mirror.example.com
docker exec git-mirror git-mirror config set listen :8443
docker exec git-mirror git-mirror config set tls_mode acme
docker exec git-mirror git-mirror config set tls_acme_hosts mirror.example.com
docker exec git-mirror git-mirror config set tls_acme_email ops@example.com
docker exec git-mirror git-mirror config set tls_acme_http :8081
docker restart git-mirror
```

The same as `compose.yaml`:

```yaml
services:
    git-mirror:
        image: ghcr.io/yousysadmin/git-mirror
        restart: unless-stopped
        ports:
            - "443:8443" # git over HTTPS, listen :8443
            - "80:8081" # ACME HTTP-01 challenge, tls_acme_http :8081
            - "2222:2222" # git over SSH
        volumes:
            - git-mirror:/data
volumes:
    git-mirror:
```

If issuance fails, the reason is in the server log. The usual suspects are DNS not pointing to
the host yet, port 80 blocked by a firewall, or a host name missing from `tls_acme_hosts`.

### SSH only

No tokens, no certificates, just keys.

```sh
git-mirror config set listen ""              # or run the server with --listen -
git-mirror config set ssh_listen :2222
git-mirror keys add ~/.ssh/id_ed25519.pub
curl -s https://github.com/alice.keys | git-mirror keys add -
# restart the server
```

```sh
git clone ssh://git@mirror.internal:2222/go.git
```

The host key is generated on the first start (`<data-dir>/ssh_host_ed25519_key`), its
fingerprint is in the server log. In a fully trusted network you can skip the key list and
accept any key: `git-mirror config set ssh_allow_any_key true`.

### Private upstreams over SSH

Generate a key inside the data dir, add the public part as a read-only deploy key upstream and
make it the default:

```sh
git-mirror identity gen deploy               # prints the public key
git-mirror config set ssh_key deploy         # default for every ssh upstream

git-mirror add git@github.com:acme/backend.git
git-mirror add git@gitlab.com:acme/infra.git --ssh-key gitlab   # another key for one repo
```

Host keys of the upstreams are learned on first use and stored in `<data-dir>/known_hosts`, so
the service user needs no `~/.ssh` at all. For special cases (per-host options, jump hosts,
custom ciphers) put an OpenSSH client config into the data dir with `git-mirror sshconfig edit`.
More in [Fetching over SSH](#fetching-over-ssh).

### Private upstreams over HTTPS

git never prompts for a password inside the server. Two options:

```sh
# preferred: a credential helper for the service user
sudo -u git git config --global credential.helper store
echo "https://oauth2:<token>@gitlab.com" | sudo -u git tee -a /home/git/.git-credentials

# simple: the token in the URL
git-mirror add https://oauth2:<token>@gitlab.com/acme/infra.git --name infra
```

A token in the URL ends up in `config.json` (world readable) and in the mirror's git config.
Logs redact it, but restrict the data dir (`chmod 750`) if you go this way.

### Behind a corporate proxy

One setting covers both HTTPS and SSH upstreams:

```sh
git-mirror config set proxy http://proxy.corp:3128      # http, https, socks5, socks5h
git-mirror config set proxy_auth user:password          # stored in <data-dir>/proxy_auth (0600)
git-mirror config set proxy_insecure true               # https proxy with a private CA

git-mirror edit internal-repo --proxy none              # this repo goes direct
git-mirror edit other --proxy socks5://socks.corp:1080  # this repo uses another proxy
```

HTTPS upstreams get the proxy as git `http.proxy`, SSH upstreams go through a `ProxyCommand`
built into git-mirror (HTTP CONNECT and SOCKS5). Prefer `proxy_auth` over credentials in the
URL, `config.json` is world readable. Proxy errors are shown as the sync error of the
repository.

### Per-team access

Tokens and SSH keys can be limited to some mirrors. A scope is a list of names or `*` patterns,
an empty scope means everything.

```sh
git-mirror config set name_style owner                  # names like acme/backend

git-mirror token add ci                                 # everything
git-mirror token add frontend --repos 'acme/web*,vendor/*'
git-mirror keys add alice.pub --repos 'acme/*'
git-mirror token ls -l                                  # which mirrors each token can see
```

`curl -u git:<token> https://mirror/` lists the mirrors a token may clone.

### Mirroring only some branches

```sh
git-mirror add https://github.com/kubernetes/kubernetes.git --branches 'master,release-1.3*'
git-mirror edit kubernetes --branches all               # back to every branch
```

Tags are always mirrored.

### config.json

You normally do not edit it by hand, but this is what a configured server looks like
(`<data-dir>/config.json`):

```json
{
    "version": 1,
    "settings": {
        "sync_interval": "15m0s",
        "sync_timeout": "30m0s",
        "workers": 4,
        "listen": ":443",
        "public_host": "mirror.example.com",
        "tls": {
            "mode": "acme",
            "acme_email": "ops@example.com",
            "acme_hosts": ["mirror.example.com"],
            "acme_http_addr": ":80"
        },
        "name_style": "owner",
        "ssh_listen": ":2222",
        "ssh_key": "deploy",
        "proxy": "http://proxy.corp:3128"
    },
    "repos": [
        {
            "name": "golang/go",
            "url": "https://github.com/golang/go.git",
            "enabled": true,
            "added": "2026-11-12T10:00:00Z"
        },
        {
            "name": "acme/backend",
            "url": "git@github.com:acme/backend.git",
            "interval": "5m0s",
            "branches": ["main", "release/*"],
            "enabled": true,
            "added": "2026-11-12T10:05:00Z",
            "proxy": "none"
        }
    ]
}
```

The `tls` block for the other modes:

```jsonc
// self-signed
"tls": { "mode": "self", "fqdn": "mirror.internal", "alg": "ecdsa" }

// own certificate
"tls": { "mode": "manual", "cert": "/etc/git-mirror/tls/fullchain.pem", "key": "/etc/git-mirror/tls/privkey.pem", "watch": true }
```

If you edit the file by hand, the server notices the change on the next tick. Use
`git-mirror config set` when you can, it validates the values and takes the lock.

## Settings reference

| Key                 | Default       | Restart | Meaning                                                          |
| ------------------- | ------------- | ------- | ---------------------------------------------------------------- |
| `interval`          | `15m`         |         | default time between syncs of one repository                     |
| `timeout`           | `30m`         |         | limit for a single clone or fetch                                |
| `workers`           | `2`           | yes     | repositories synced in parallel                                  |
| `name_style`        | `repo`        |         | how names are derived from URLs: `repo`, `owner`, `full`         |
| `listen`            | `:8080`       | yes     | HTTP(S) address, empty turns it off                              |
| `public_host`       | host name     |         | host used in clone URLs and as the self-signed certificate name  |
| `tls_mode`          | `none`        | yes     | `none`, `self`, `manual`, `acme`                                 |
| `tls_fqdn`          | `public_host` | yes     | `self`: name in the certificate                                  |
| `tls_alg`           | `ecdsa`       | yes     | `self`: `ecdsa`, `rsa`, `ed25519`                                |
| `tls_cert`          |               | yes     | `manual`: PEM certificate (full chain)                           |
| `tls_key`           |               | yes     | `manual`: PEM private key                                        |
| `tls_watch`         | `false`       | yes     | `manual`: reload the files when they change                      |
| `tls_acme_hosts`    |               | yes     | `acme`: allowed host names, comma separated                      |
| `tls_acme_email`    |               | yes     | `acme`: contact e-mail for the CA                                |
| `tls_acme_http`     | `:80`         | yes     | `acme`: HTTP-01 challenge listener, must be reachable as port 80 |
| `ssh_listen`        | `:2222`       | yes     | built-in SSH server address, empty turns it off                  |
| `ssh_allow_any_key` | `false`       |         | accept every client key, ignore `authorized_keys`                |
| `ssh_key`           | empty         |         | default identity (or absolute key path) for SSH upstreams        |
| `proxy`             | empty         |         | proxy for upstream fetches, `none` to clear                      |
| `proxy_auth`        | empty         |         | `user:password` for the proxy, stored in `<data-dir>/proxy_auth` |
| `proxy_insecure`    | `false`       |         | skip certificate checks of an `https://` proxy                   |

Per repository you can override `interval`, `branches`, `ssh_key` and `proxy` with
`git-mirror add` or `git-mirror edit`.

## Cloning from the mirror

Both protocols are read-only, a push is always rejected.

### HTTP(S)

Every request needs a token: as the password of HTTP basic auth (the user name does not
matter) or as `Authorization: Bearer <token>`. No token gives 401, a token without access to
that mirror gives 403, an unknown mirror gives 404.

```sh
git clone https://git:<token>@mirror.internal/go.git

# or let git remember it
git config --global credential.helper store
git clone https://mirror.internal/go.git        # asks once, user: anything, password: token
```

Tokens have names and live in `<data-dir>/tokens.json` (0600). A token called `default` with
access to everything is created on the first start with HTTP enabled.

```sh
git-mirror token                          # the value, when there is only one token
git-mirror token ls [-l]                  # name, scope, created, value (-l: matching mirrors)
git-mirror token add ci --repos go,team/* # new token with a random value (--value sets your own)
git-mirror token show ci                  # value only, handy in scripts
git-mirror token edit ci --repos all      # change the scope
git-mirror token rotate ci                # new value, the old one stops working at once
git-mirror token rm ci
```

Upgrading from an old version with a single `http_token` file: it is read as the token
`default` until the first token command writes `tokens.json` and renames the old file to
`http_token.migrated`.

### Built-in SSH server

A minimal SSH server inside `git-mirror server`. It only runs `git-upload-pack` and
`git-upload-archive` for registered mirrors. Shells, port forwarding and everything else are
refused. The user name in the URL does not matter, `git` is a convention.

```sh
git-mirror keys add ~/.ssh/id_ed25519.pub                 # applied immediately
git-mirror keys add "ssh-ed25519 AAAA... alice@laptop" --repos 'team/*'
git-mirror keys ls                                        # type, fingerprint, scope, comment
git-mirror keys edit alice@laptop --repos all
git-mirror keys rm alice@laptop                           # by comment or fingerprint
```

Keys are stored in `<data-dir>/authorized_keys` in the usual OpenSSH format. A scope is written
as an option:

```
repos="team/*" ssh-ed25519 AAAAC3Nza... alice@laptop
```

Only `repos=`, `restrict` and the `no-*` options are understood. A key with an option the
server cannot enforce (`from=`, `command=`, `expiry-time=`, ...) is refused by `keys add` and
rejected at login if someone put it in the file by hand. Removing a key or changing its scope
also affects connections that are already open, starting with their next session.

Older git-mirror versions drop the `repos=` option when they rewrite the file, so do not run
different versions against one data dir.

### System sshd instead

The mirrors are plain bare repositories, so the system OpenSSH can serve them too. Do **not**
hand out the service user `git`: it owns the mirrors and could push into them. Use a separate
read-only user:

```sh
sudo useradd -m -G git -s "$(command -v git-shell)" git-ro
# add client keys to ~git-ro/.ssh/authorized_keys, each line prefixed with "restrict "
git clone git-ro@mirror.internal:/home/git/.local/share/git-mirror/repos/go.git
```

`git-ro` needs to traverse the home of `git`: `chmod 0750 /home/git /home/git/.local /home/git/.local/share`.

The price: full paths in the URL, an extra OS user, no per-key scopes, and push is blocked only
by file permissions. The built-in server is simpler to run.

## How syncing works

- **Everything is mirrored**: all branches and tags (`+refs/*:refs/*`, like
  `git clone --mirror`). Branches and tags deleted upstream are deleted from the mirror on the
  next sync (`git fetch --prune --prune-tags`).
- **Branch filter**: with `--branches main,release/*` only those branches (plus all tags) are
  fetched, branches that stop matching are removed. The mirror `HEAD` follows the upstream
  default branch, or the first mirrored branch if the default one is filtered out, so a plain
  `git clone` keeps working.
- **First sync** is `git init --bare` plus a fetch into a temporary directory, then a rename into
  `repos/<name>.git`. A failed first sync never leaves a half-done mirror behind.
- **Later syncs** run `git fetch --prune` every `interval` (global or per repository), up to
  `workers` at a time, each limited by `timeout`.
- **No prompts**: `GIT_TERMINAL_PROMPT=0` is always set. Missing credentials show up as a sync
  error of the repository.
- **Without a server**: `git-mirror add`, `sync` and the TUI run git themselves when the server
  is not running and update `state.json` the same way.

### Fetching over SSH

With an identity from `<data-dir>/identities` the server runs ssh roughly as

```
ssh -i <data-dir>/identities/<name> -o IdentitiesOnly=yes \
    -o UserKnownHostsFile=<data-dir>/known_hosts \
    -o BatchMode=yes -o StrictHostKeyChecking=accept-new
```

```sh
git-mirror identity gen deploy               # new ed25519 key, prints the public key
git-mirror identity add laptop ~/.ssh/id_ed25519   # import an existing key (no passphrase)
git-mirror identity ls                       # name, type, fingerprint, which repos use it
git-mirror identity pub deploy               # public key again
git-mirror identity default deploy           # same as config set ssh_key deploy
git-mirror edit org/private --ssh-key default
git-mirror identity rm deploy [--force]      # refused while something still uses it
```

`ssh_key` also accepts an absolute path to a key outside the data dir. Then the user's
`~/.ssh/known_hosts` is used. With no key at all the plain `ssh` of the service user applies.

`<data-dir>/ssh_config` is a normal OpenSSH client config. When it exists, ssh is run with
`-F <data-dir>/ssh_config`, so `~/.ssh/config` and `/etc/ssh/ssh_config` are ignored:

```sh
git-mirror sshconfig edit      # opens $EDITOR, creates the file with a commented header
git-mirror sshconfig show | path | rm
```

```
Host github.com
    ProxyCommand corkscrew-go proxy http://proxy.internal:8080 %h %p
```

A `ProxyCommand` from the `proxy` setting wins over one in `ssh_config`.

## Mirror names

A name identifies a mirror everywhere: the directory under `repos/`, CLI arguments and clone
URLs. Without `--name` it is derived from the URL according to `name_style`:

| `name_style`     | `https://github.com/golang/go.git` becomes |
| ---------------- | ------------------------------------------ |
| `repo` (default) | `go`                                       |
| `owner`          | `golang/go`                                |
| `full`           | `github.com/golang/go`                     |

Names may contain `/`, so `--name team/go` always works. If two URLs derive to the same name,
`add` fails and asks for `--name`. Changing `name_style` only affects repositories added later.

## TUI

`git-mirror tui` (or just `git-mirror` in a terminal) shows the list of mirrors with their
status, details of the selected one and, on the left, the server state and settings. It
refreshes every 2 seconds.

| Key           | Action                                                                  |
| ------------- | ----------------------------------------------------------------------- |
| `a`           | add a repository (URL, name, interval, branches, key, proxy, access)    |
| `d`           | remove (with confirmation)                                              |
| `s` / `enter` | sync now                                                                |
| `S`           | sync all                                                                |
| `e`           | edit interval, branches, SSH identity, proxy (`i` / `b` jump directly)  |
| `t`           | enable / disable                                                        |
| `c`           | clone URLs, a digit copies one to the clipboard (OSC 52)                |
| `T`           | HTTP tokens: `a` add, `e` scope, `R` rotate, `d` remove, `y` copy       |
| `K` / `tab`   | SSH client keys: `a` add, `e` scope, `d` remove                         |
| `I`           | SSH identities: `g` generate, `a` import, `D` default, `y` copy pub key |
| `/`           | filter                                                                  |
| `r`           | refresh                                                                 |
| `?`           | help: all keys, status icons, endpoints, settings, data dir layout      |
| `q`           | quit                                                                    |

## Command reference

```
git-mirror init                            # data dir and initial config.json
git-mirror server [--listen :8080] [--ssh-listen :2222] [--tick 15s] [--json] [--debug]
git-mirror tui

git-mirror add <url>... [--name n] [--interval 1h] [--branches main,release/*]
                        [--ssh-key id] [--proxy url] [--disabled] [--no-sync]
git-mirror edit <name> [--url u] [--interval d|default] [--branches list|all]
                       [--ssh-key id|path|default] [--proxy url|none|default]
git-mirror rm <name>... [--purge]          # --purge also deletes the files
git-mirror ls [-l]
git-mirror status
git-mirror sync [<name>...] [--local]      # through the server, or locally
git-mirror enable|disable <name>...

git-mirror config                          # show settings
git-mirror config set <key> <value>        # see the settings reference

git-mirror token [ls|show|add|edit|rotate|rm|set] ...
git-mirror keys add|ls|edit|rm ...
git-mirror identity ls|gen|add|pub|rm|default ...
git-mirror sshconfig show|edit|path|rm
git-mirror cert [--fingerprint]            # certificate of the HTTPS endpoint (self, manual)
git-mirror url <name> [--ssh] [--token] [--command]
git-mirror path [<name>]                   # data dir or mirror path

Global: --data-dir / -d, or $GIT_MIRROR_DIR (default ~/.local/share/git-mirror)
```

## Data dir

Everything git-mirror knows lives in one directory. The CLI and the server share it and
coordinate through file locks, so both can write at the same time. Back it up and you have
backed up everything.

```
$GIT_MIRROR_DIR/              default ~/.local/share/git-mirror
|-- config.json               repositories and settings
|-- state.json                sync status of every mirror
|-- lock                      file lock for config.json and state.json
|-- server.sock               control API of the running server
|-- tokens.json               HTTP tokens (0600)
|-- authorized_keys           client keys for the built-in SSH server
|-- ssh_host_ed25519_key      host key of the built-in SSH server
|-- identities/               private keys for SSH upstreams
|-- known_hosts               upstream host keys
|-- ssh_config                optional OpenSSH client config for fetches
|-- proxy_auth                optional proxy credentials (0600)
|-- certs/                    self-signed certificate, ACME cache
`-- repos/
    `-- go.git                bare mirror
```

## Control API

The server listens on `<data-dir>/server.sock` (JSON over a unix socket). The CLI and TUI use
it, scripts can too:

| Request                       | Does                              |
| ----------------------------- | --------------------------------- |
| `GET /v1/ping`                | is the server alive               |
| `GET /v1/status`              | settings and state of all mirrors |
| `POST /v1/sync?name=a&name=b` | queue a sync                      |
| `POST /v1/reload`             | re-read `config.json`             |

```sh
curl --unix-socket "$(git-mirror path)/server.sock" http://x/v1/status
```

## Development

```sh
task                    # list all tasks
task build test lint
task check              # vet, lint, test, fmt, typos, shellcheck, goreleaser check
task run-server         # server on 127.0.0.1:8080 with a throwaway ./.data
task run-tui            # TUI against the same ./.data
task snapshot           # local GoReleaser dry run
task hooks              # install git hooks (lefthook)
```
