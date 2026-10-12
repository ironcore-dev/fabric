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
    config interface mtu Ethernet0 9100
    config interface fec Ethernet0 rs
    config interface speed Ethernet0 100000
    config interface startup Ethernet0
    config interface mtu Ethernet4 9100
    config interface fec Ethernet4 rs
    config interface speed Ethernet4 100000
    config interface startup Ethernet4
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
    config vlan add 1001
    config vlan member add 1001 Ethernet0 -u
    config interface ip add Vlan1001 fd00:1234:5678:f2:1::/80
    config vlan dhcp_relay add 1001 fd00:1234:5678:3201::1:547
    config vlan add 1002
    config vlan member add 1002 Ethernet4 -u
    config interface ip add Vlan1002 fd00:1234:5678:f2:5::/80
    config vlan dhcp_relay add 1002 fd00:1234:5678:3201::1:547

    # Enable IPv6 link-local
    config ipv6 enable link-local

    # Save running config
    config save -y

    # Update FRR (BGP) configuration
    cat <<EOF >/etc/sonic/frr/frr.conf
frr version 8.1
frr defaults traditional
hostname swi1-ab-42.fabric.example.com
log syslog informational
service integrated-vtysh-config

interface Vlan1001
  no ipv6 nd suppress-ra
  ipv6 nd managed-config-flag
  ipv6 nd other-config-flag

interface Vlan1002
  no ipv6 nd suppress-ra
  ipv6 nd managed-config-flag
  ipv6 nd other-config-flag
router bgp 4211000042
  bgp router-id 1.0.0.42
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

  neighbor SOUTH peer-group
  neighbor SOUTH remote-as external
  neighbor SOUTH timers 3 9
  neighbor SOUTH timers connect 20
  neighbor Vlan1001 interface peer-group SOUTH
  neighbor Vlan1002 interface peer-group SOUTH

  address-family ipv6 unicast
    network fd00:1234:5678:f2::/64

    neighbor FABRIC activate
    neighbor FABRIC route-map RM_FABRIC_IN in
    neighbor FABRIC route-map RM_FABRIC_OUT out

    neighbor SOUTH activate
    neighbor SOUTH route-map RM_SOUTH_IN in
    neighbor SOUTH route-map RM_SOUTH_OUT out
  exit-address-family
exit

route-map RM_FABRIC_IN permit 10
  set community 65000:100
!
bgp community-list 10 permit 65000:100

route-map RM_FABRIC_OUT deny 10
  match community 10
route-map RM_FABRIC_OUT permit 20
!
route-map RM_SOUTH_IN permit 10
route-map RM_SOUTH_OUT permit 10
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
