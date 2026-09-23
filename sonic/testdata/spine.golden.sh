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

# Flag file to track completion
FLAG_FILE_DONE="/etc/sonic/fabric-cell.done"

if [ ! -f "$FLAG_FILE_DONE" ]; then
    # Configure the loopback addresses handed down by the cell
    config interface ip add Loopback0 fd00:1234:5678:f2::/128

    # Configure the physical settings of all peer ports named by the cell
    config interface mtu Ethernet108 9100
    config interface fec Ethernet108 rs
    config interface speed Ethernet108 100000
    config interface startup Ethernet108
    config interface mtu Ethernet112 9100
    config interface fec Ethernet112 rs
    config interface speed Ethernet112 100000
    config interface startup Ethernet112

    # Configure the south-facing server ports: one VLAN per port with its
    # interface prefix and DHCP relay

    # Enable IPv6 link-local
    config ipv6 enable link-local

    # Save running config
    config save -y

    # Update FRR (BGP) configuration
    cat <<EOF >/etc/sonic/frr/frr.conf
frr version 8.1
frr defaults traditional
hostname swi2-ab-7.fabric.example.com
log syslog informational
service integrated-vtysh-config
router bgp 4212000042
  bgp router-id 2.0.0.42
  no bgp ebgp-requires-policy
  no bgp default ipv4-unicast
  bgp bestpath as-path multipath-relax
  no bgp network import-check

  neighbor FABRIC peer-group
  neighbor FABRIC remote-as external
  neighbor FABRIC timers 3 9
  neighbor FABRIC timers connect 20
  neighbor Ethernet108 interface peer-group FABRIC
  neighbor Ethernet112 interface peer-group FABRIC

  address-family ipv6 unicast
    network fd00:1234:5678:f2::/64

    neighbor FABRIC activate
    neighbor FABRIC route-map RM_FABRIC_IN in
    neighbor FABRIC route-map RM_FABRIC_OUT out

  exit-address-family
exit

route-map RM_FABRIC_IN permit 10
route-map RM_FABRIC_OUT permit 10
!
ipv6 route fd00:1234:5678:f2::/64 reject
EOF

    # Restart BGP to load new config
    systemctl restart bgp

    # Mark the cell as applied
    touch "$FLAG_FILE_DONE"
    reboot
fi
exit 0
