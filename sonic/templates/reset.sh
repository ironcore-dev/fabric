#!/bin/bash
# SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
# SPDX-License-Identifier: Apache-2.0
set -e

# When run inside a container, re-exec in the host namespaces so the
# config/systemctl/reboot calls below act on the host.
if [ -z "$FABRIC_ON_HOST" ]; then
    export FABRIC_ON_HOST=1
    exec nsenter --target 1 --mount --net --pid bash "$0"
fi

# Reset script for node {{ .Node }}, rendered by the fabric cell runtime.
# Undoes what the cell script applied: server VLANs, loopback IPs, port
# state and the FRR/BGP config. Everything is read from the running
# CONFIG_DB, so this also works on a partially provisioned device (failed
# cells). Deliberately kept: hostname, management config, link-local and
# the physical port parameters (mtu/fec/speed).

CONFIG_DB=/etc/sonic/config_db.json

# Remove the loopback addresses handed down by the cell
for ENTRY in $(jq -r '.LOOPBACK_INTERFACE // {} | keys[] | select(contains("|"))' "$CONFIG_DB"); do
    config interface ip remove "${ENTRY%%|*}" "${ENTRY#*|}"
done

# Remove the VLANs the cell's server peers created, in dependency order:
# DHCP relay, interface IPs, members, then the VLAN itself
for VLAN in $(jq -r '.VLAN // {} | keys[]' "$CONFIG_DB"); do
    VLAN_ID="${VLAN#Vlan}"
    for SERVER in $(jq -r --arg v "$VLAN" '.DHCP_RELAY[$v] // {} | .dhcpv6_servers // .dhcp_servers // [] | .[]' "$CONFIG_DB"); do
        config vlan dhcp_relay del "$VLAN_ID" "$SERVER"
    done
    for ENTRY in $(jq -r --arg v "$VLAN" '.VLAN_INTERFACE // {} | keys[] | select(startswith($v + "|"))' "$CONFIG_DB"); do
        config interface ip remove "$VLAN" "${ENTRY#*|}"
    done
    for MEMBER in $(jq -r --arg v "$VLAN" '.VLAN_MEMBER // {} | keys[] | select(startswith($v + "|"))' "$CONFIG_DB"); do
        config vlan member del "$VLAN_ID" "${MEMBER#*|}"
    done
    config vlan del "$VLAN_ID"
done

# Shut down every front panel port the cell script started up
for PORT in $(jq -r '.PORT // {} | to_entries[] | select(.value.admin_status == "up") | .key' "$CONFIG_DB"); do
    config interface shutdown "$PORT"
done

config save -y

# Return FRR to an empty config: no BGP peering
cat <<EOF >/etc/sonic/frr/frr.conf
frr version 8.1
frr defaults traditional
hostname $(hostname)
log syslog informational
service integrated-vtysh-config
EOF

systemctl restart bgp

# Mark the device as unprovisioned, let the fabriclet expire the cell
rm -f /etc/sonic/fabric-cell.done

reboot
