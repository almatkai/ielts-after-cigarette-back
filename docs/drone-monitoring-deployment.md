# Shared Drone monitoring deployment contract

Production hosts with `~/drone-league/monitoring/require-ielts-integration`
require a coherent installation of Drone monitoring guard modules outside the
IELTS release directory. The marker is persistent operational state; never ship
it in a build or regenerate webhook credentials during release.

Uploads extract to a private staging directory. `install-production-bundle.py`
runs the guard preparation there before any live overwrite, snapshots replaced
files privately under `~/ielts-deployment-backups`, and writes existing files in
place so Docker configuration bind mounts retain their inodes. Deploy then
rebuilds the existing bridge, recreates Prometheus as before, validates/reloads
Alloy, and requires healthy guard verification before reporting success.
Hosts without the marker keep the existing base/operational deployment flow.

The guard source is maintained in `almatkai/drone-league`, branch
`fix/monitoring-redeploy-durability`, commit
`3d9fd1eadaed377de2f2de786f213d12900e9cff`. Keep the deployed `drone.alloy`
template pinned to the approved running pipeline; changing log fields is a
separate operation. No secret files, outbox state, volumes, routes or Docker
networks are replaced by this integration.

A failed upload/verification must stop the release. Rollback restores the
specific replaced files from its private snapshot with in-place copies, then
uses the existing approved service rollback. Do not remove monitoring volumes
or reset the webhook secret. The upload snapshot does not replace a database
backup. Regression tests run in GitHub Actions; do not run them on the MacBook.
