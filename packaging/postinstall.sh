#!/bin/sh
cat <<'MSG'

drift installed. Each user turns it on with:

    systemctl --user enable --now drift.service

Then set your desktop's own "turn screen off" timer longer than drift's
idle_minutes (default 5) so drift runs first. Configure with:

    drift init        # writes ~/.config/drift/drift.conf
    drift list        # see available savers
    drift preview pipes

MSG
