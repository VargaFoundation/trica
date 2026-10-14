#!/bin/sh
# Stop the service when the package is removed or upgraded to a new package
# (the new files are laid down after this script). The state directory
# /var/lib/trica-agent and the user are left alone: nothing is deleted.
set -e
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
	systemctl stop trica-agent.service || true
fi
