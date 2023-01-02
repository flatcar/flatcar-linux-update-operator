# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased
### Changed
- Moved from `github.com/flatcar-linux/flatcar-linux-update-operator` to `github.com/flatcar/flatcar-linux-update-operator`. This also means that the docker images will be now available at `ghcr.io/flatcar/flatcar-linux-update-operator`. The `0.8.0` image is still available at the old location, but no new images will be pushed there.

## [0.8.0] - 2021-09-24
### Added
- Added golangci-lint and codespell CI jobs using GitHub Actions.
- Added CI for vendor directory, generated files, Go modules tidiness, Dockerfile etc.
- Added missing documentation for all packages.
- Added integration tests for `pkg/updateengine`.
- Added unit tests for `pkg/operator`.

### Changed
- Updated used Go version to 1.16 and Alpine version for container images to 3.14.
- Container images will be now published to `ghcr.io/flatcar-linux/flatcar-linux-update-operator` instead of
`quay.io/kinvolk/flatcar-linux-update-operator`.
- `update-agent` now use a dedicated Service Account with lower privileges than the operator.
- `update-agent` now use a dedicated Pod Security Policy.
- `update-operator` now use more strict Pod Security Policy, as it no longer needs permissions
for agent related resources.
- `Flatcar Linux` references has been changed to `Flatcar Container Linux`.
- Migrated from [glog](https://github.com/golang/glog) to [klog](https://github.com/kubernetes/klog),
which is a recommended logger for Kubernetes operators.
- `pkg/drain` has been integrated into `pkg/k8sutil`.
- Errors returned from operator and agent runs are now properly wrapped and can be unwrapped by the user.
- Remaining CoreOS references has been replaced with Flatcar.
- `pkg/k8sutil.GetVersionInfo()` will now return an error when `/etc/flatcar/update.conf` file is present,
but not readable.
- Marking node as Unschedulable should now be more robust on frequent Node object updates.
- Renamed Go module from `github.com/kinvolk/flatcar-linux-update-operator` to `github.com/flatcar-linux/flatcar-linux-update-operator`, following the move of repository into new organization.

### Fixed
- Fixed various typos found by codespell.
- Operator will no longer leak leader election goroutine, it will be now shut down when stop channel gets closed.
- Operator will now internally gracefully return an error when leader election is lost, so process can be restarted
to start a new leader election.

### Removed
- Removed no longer used Travis CI, Jenkins configuration files and build scripts.
- Removed `AttemptUpdate()`, `GetStatus()` and `RebootNeededSignal()` functions from `pkg/updateengine` package.
- Removed deprecated `--manage-agent`, `--agent-image-repo` and `--analytics` flags from `update-operator`.
- Deprecated operator functionality to manage agent DaemonSet has been removed. Agent DaemonSet must be now deployed separately.

## [0.7.3] - 2020-01-14
### Changed
- Migrated to Go modules.
- Updated all Go dependencies to latest versions.
- Use vendored dependencies while building binaries.
- `update-agent` example manifests now enforce running agent as root, to ensure agent
is able to trigger a reboot on the host.

### Fixed
- Fixed example manifests for Kubernetes 1.17 compatibility.
- Made operator code compatible with Kubernetes 1.17.
- Fixed publishing leader acquired event by the operator.

[0.8.0]: https://github.com/flatcar/flatcar-linux-update-operator/compare/v0.7.3...v0.8.0
[0.7.3]: https://github.com/flatcar/flatcar-linux-update-operator/compare/v0.7.2...v0.7.3
