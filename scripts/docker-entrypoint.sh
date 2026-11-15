#!/bin/sh
# Apply pending migrations, then exec the API (or the Railway start command).
# Production keeps AUTO_MIGRATE=false in the Go process (config.Validate forbids
# in-process migrate), so a new migration used to crash the deploy with
# "refusing to serve: N migration(s) not applied". Packaging migrate into the
# image boot is the out-of-band step Railway needs.
set -eu
/app/migrate
if [ "$#" -eq 0 ]; then
  exec /app/mes
fi
exec "$@"
