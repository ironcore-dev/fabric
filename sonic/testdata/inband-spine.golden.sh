#!/bin/bash
# SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
# SPDX-License-Identifier: Apache-2.0
set -e

# =============================
# 1) CONFIGURABLE VARIABLES
# =============================
HOSTNAME_PREFIX="swi2-ab"
SPINE_ID="42"
ASN_BASE=4212000000
IPV6_BASE="fd00:1234:5678"
SEARCH_DOMAIN="fabric.example.com"

# Flag file to track completion
FLAG_FILE_DONE="/etc/sonic/fabric-cell.done"

if [ ! -f "$FLAG_FILE_DONE" ]; then
    echo "=== ZTP: Starting configuration ==="

    # Configure Loopback0 with the first IP address out of the base prefix, using a /128 subnet
    config interface ip add Loopback0 ${IPV6_BASE}:10$((SPINE_ID))::/128

    # 2. Configure VLANs, FEC, MTU, IP addresses, etc. for Ethernet0..103
    for i in {0..124..4}; do
        config interface mtu Ethernet$i 9100
        config interface fec Ethernet$i rs
        config interface speed Ethernet$i 100000

        config interface startup Ethernet$i
    done

    # 4. Enable IPv6 link-local
    config ipv6 enable link-local

    # 5. Save running config
    config save -y

    # 6. Update FRR (BGP) configuration
    cat <<EOF >/etc/sonic/frr/frr.conf
frr version 8.1
frr defaults traditional
hostname ${HOSTNAME_PREFIX}-${SPINE_ID}.${SEARCH_DOMAIN}
log syslog informational
service integrated-vtysh-config

router bgp $((SPINE_ID+ASN_BASE))
  bgp router-id 2.0.$((SPINE_ID / 256)).$((SPINE_ID % 256))
  no bgp ebgp-requires-policy
  no bgp default ipv4-unicast
  bgp bestpath as-path multipath-relax
  no bgp network import-check

  neighbor LEAFS peer-group
  neighbor LEAFS remote-as external
  neighbor LEAFS timers 3 9
  neighbor LEAFS timers connect 20
$(for ETH_PORT in {0..124..4}; do
  echo "  neighbor Ethernet$(($ETH_PORT)) interface peer-group LEAFS"
done)

  address-family ipv6 unicast
    network ${IPV6_BASE}:10$((SPINE_ID))::/64

    neighbor LEAFS activate
    neighbor LEAFS route-map RM_LEAFS_IN in
    neighbor LEAFS route-map RM_LEAFS_OUT out
  exit-address-family
exit

route-map RM_LEAFS_IN permit 10
route-map RM_LEAFS_OUT permit 10
!
ipv6 route ${IPV6_BASE}:10$((SPINE_ID))::/64 reject
EOF

    # 7. Restart BGP to load new config
    systemctl restart bgp

    # 8. Stop ZTP daemon
    touch "$FLAG_FILE_DONE"
    echo "=== ZTP complete. Provisioning done, rebooting! ==="
    reboot
fi
exit 0
