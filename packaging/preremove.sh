#!/bin/sh
# Stop running user daemons on uninstall (best effort; users re-login otherwise).
pkill -x drift 2>/dev/null || true
exit 0
