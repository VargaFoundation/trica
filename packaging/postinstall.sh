#!/bin/sh
# Create the unprivileged user and load the unit. The service is neither enabled
# nor started: the agent needs its server and update key configured first.
set -e
if command -v systemd-sysusers >/dev/null 2>&1; then
	systemd-sysusers /usr/lib/sysusers.d/trica-agent.conf || true
fi
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
	systemctl daemon-reload || true
fi
echo "trica-agent installed: set the server in /etc/trica-agent/agent.env, then 'systemctl enable --now trica-agent'."
