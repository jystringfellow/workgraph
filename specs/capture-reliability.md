# Capture Reliability

Transient filesystem disappearance during watch registration or event delivery
must not stop capture. Missing paths are logged and skipped; unrelated watcher
errors remain fatal and visible in daemon status. Subsequent durable file
activity must still be captured.
