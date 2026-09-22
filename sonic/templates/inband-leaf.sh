#!/bin/bash
# SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
# SPDX-License-Identifier: Apache-2.0
set -e

# =============================
# 1) CONFIGURABLE VARIABLES
# =============================
HOSTNAME_PREFIX="swi1-__region__"
LEAF_ID="__id__"
ASN_BASE=4211000000
IPV6_BASE="__ipv6_base__"
SEARCH_DOMAIN="__search_domain__"

# Flag file to track completion
FLAG_FILE_DONE="/etc/sonic/fabric-cell.done"

if [ ! -f "$FLAG_FILE_DONE" ]; then
    echo "=== ZTP: Starting configuration ==="

    # Configure Loopback0 with the first IP address out of the base prefix, using a /128 subnet
    config interface ip add Loopback0 ${IPV6_BASE}:$((LEAF_ID+200))::/128

    # Configure VLANs, FEC, MTU, IP addresses, etc. for Ethernet0..104
    for i in {0..104..4}; do
        config interface mtu Ethernet$i 9100
        config interface fec Ethernet$i rs
        config interface speed Ethernet$i 100000

        VLAN_ID=$((i/4+1001))
        config vlan add "$VLAN_ID"
        config vlan member add "$VLAN_ID" Ethernet$i -u
        config interface ip add Vlan"$VLAN_ID" ${IPV6_BASE}:$((LEAF_ID+200)):$(printf '%04x' $((i+1)))::/80
        config vlan dhcp_relay add "$VLAN_ID" ${IPV6_BASE}:3201::1:547
        config interface startup Ethernet$i
    done

    # Configure VLANs, FEC, MTU, IP addresses, etc. for Ethernet108..124
    for i in {108..124..4}; do
        config interface mtu Ethernet$i 9100
        config interface fec Ethernet$i rs
        config interface speed Ethernet$i 100000
        config interface startup Ethernet$i
    done

    # Enable IPv6 link-local
    config ipv6 enable link-local

    # Save running config
    config save -y

    # 6. Update FRR (BGP) configuration
    cat <<EOF >/etc/sonic/frr/frr.conf
frr version 8.1
frr defaults traditional
hostname ${HOSTNAME_PREFIX}-${LEAF_ID}.${SEARCH_DOMAIN}
log syslog informational
service integrated-vtysh-config

$(for ETH_PORT in {0..104..4}; do
  echo "interface Vlan$(($ETH_PORT/4+1001))"
  echo "  no ipv6 nd suppress-ra"
  echo "  ipv6 nd managed-config-flag"
  echo "  ipv6 nd other-config-flag"
  echo ""
done)

router bgp $((LEAF_ID+ASN_BASE))
  bgp router-id 1.0.$((LEAF_ID / 256)).$((LEAF_ID % 256))
  no bgp ebgp-requires-policy
  no bgp default ipv4-unicast
  bgp bestpath as-path multipath-relax
  no bgp network import-check

  neighbor NORTH peer-group
  neighbor NORTH remote-as external
  neighbor NORTH timers 3 9
  neighbor NORTH timers connect 20
$(for ETH_PORT in {108..124..4}; do
  echo "  neighbor Ethernet$(($ETH_PORT)) interface peer-group NORTH"
done)

  neighbor SOUTH peer-group
  neighbor SOUTH remote-as external
  neighbor SOUTH timers 3 9
  neighbor SOUTH timers connect 20
$(for ETH_PORT in {0..104..4}; do
  echo "  neighbor Vlan$(($ETH_PORT/4+1001)) interface peer-group SOUTH"
done)

  address-family ipv6 unicast
    network ${IPV6_BASE}:$((LEAF_ID+200))::/64

    neighbor NORTH activate
    neighbor NORTH route-map RM_NORTH_IN in
    neighbor NORTH route-map RM_NORTH_OUT out

    neighbor SOUTH activate
    neighbor SOUTH route-map RM_SOUTH_IN in
    neighbor SOUTH route-map RM_SOUTH_OUT out
  exit-address-family
exit

route-map RM_NORTH_IN permit 10
  set community 65000:100
!
bgp community-list 10 permit 65000:100

route-map RM_NORTH_OUT deny 10
  match community 10
route-map RM_NORTH_OUT permit 20
!
route-map RM_SOUTH_IN permit 10
route-map RM_SOUTH_OUT permit 10
!
ipv6 route ${IPV6_BASE}:$((LEAF_ID+200))::/64 reject
EOF

    # 7. Restart BGP to load new config
    systemctl restart bgp

    # 8. Stop ZTP daemon
    touch "$FLAG_FILE_DONE"
    echo "=== ZTP complete. Provisioning done, rebooting! ==="
    reboot
fi
exit 0
