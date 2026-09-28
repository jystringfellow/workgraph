# Capture Reliability

Transient filesystem disappearance during watch registration or event delivery
must not stop capture. Missing paths are logged and skipped; unrelated watcher
errors remain fatal and visible in daemon status. Subsequent durable file
activity must still be captured.

## Opt-in user service

`workgraph service install [--home path]` installs and starts a per-user capture
service for that workgraph home. macOS uses launchd RunAtLoad, KeepAlive and a
10-second throttle; Linux uses systemd --user, Restart=on-failure, RestartSec=10
and default.target. No root privileges, Windows service, or Linux lingering is
configured. Installation uses the current executable's absolute path and saved
capture settings. Reinstall after moving the executable.

The service runs the daemon worker in the foreground with the same PID, status,
failure recording and logs as ordinary background capture. Installation refuses
to take over a running manually started daemon. Reinstall refreshes the existing
service. macOS logs go to daemon.log; Linux logs go to the user journal.
Start and stop control the supervisor when a service is installed;
stop disables login startup until start or install enables it again.

`service status` reports the definition path and supervisor state, separately
from capture health. `service uninstall` disables and stops the job before
removing its definition, preserving events, memory, settings and logs. Missing
installations can be uninstalled repeatedly. Supervisor failures are reported;
an installed definition alone never implies healthy capture.

Service labels include a hash of the absolute workgraph home. Commands use
argument arrays, XML escaping and systemd escaping, including paths containing
spaces, quotes, dollar signs and percent signs. Only HOME and PATH are included
in service definitions; credentials are loaded through normal saved settings.
Worker process discovery must preserve spaced home and database paths; a second
worker for the same home must fail without replacing the first worker's state.
