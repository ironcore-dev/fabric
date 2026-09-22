#!/bin/bash
# SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
# SPDX-License-Identifier: Apache-2.0
set -e

# When run inside a container, re-exec in the host namespaces so the
# reset calls below act on the host.
if [ -z "$FABRIC_ON_HOST" ]; then
    export FABRIC_ON_HOST=1
    exec nsenter --target 1 --mount --net --pid bash "$0"
fi

# Reset script for node __node__, rendered by the fabric cell runtime.
# TODO: factory-reset the switch here (ONIE re-install).
echo "Resetting the device is not implemented yet"
exit 0
