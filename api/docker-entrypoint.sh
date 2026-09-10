#!/bin/sh
set -eu

mkdir -p "${UPLOAD_DIRECTORY}"
chown documind:documind "${UPLOAD_DIRECTORY}"

su-exec documind documind-api migrate
exec su-exec documind documind-api
