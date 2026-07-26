#!/bin/sh
set -eu

mkdir -p "${UPLOAD_DIRECTORY}"
chown documind:documind "${UPLOAD_DIRECTORY}"

exec su-exec documind documind-api