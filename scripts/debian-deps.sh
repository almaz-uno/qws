#!/bin/sh
# Packages that building and testing qws needs on Debian 12 — the environment
# of CI and of release builds (specs/004-releases): git, a C toolchain for cgo,
# pkg-config and the OpenGL and X11 development packages; gh publishes
# releases. Run as root.
set -eu

apt-get update
apt-get install -y --no-install-recommends \
	ca-certificates git make gcc libc6-dev pkg-config libgl-dev libx11-dev gh
