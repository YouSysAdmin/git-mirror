#!/bin/sh
# Create the service user and data dir expected by the systemd unit.
set -e
home=/home/git
if ! getent passwd git >/dev/null 2>&1; then
  if command -v useradd >/dev/null 2>&1; then
    useradd --system --home-dir "$home" --create-home --shell /sbin/nologin git
  else
    adduser -S -D -h "$home" -s /sbin/nologin git
  fi
fi
actual=$(getent passwd git | cut -d: -f6)
if [ "$actual" != "$home" ]; then
  echo "warning: home of user git is $actual, the systemd unit expects $home" >&2
fi
mkdir -p "$home/.local/share/git-mirror"
chown git:git "$home" "$home/.local" "$home/.local/share" "$home/.local/share/git-mirror"
chmod 0750 "$home/.local/share/git-mirror"
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload >/dev/null 2>&1 || true
fi
echo "git-mirror installed. Enable the server with: systemctl enable --now git-mirror"
