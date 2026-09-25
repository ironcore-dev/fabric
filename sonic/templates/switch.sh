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
{{- range .LoopbackIPs }}
    config interface ip add Loopback0 {{ . }}/128
{{- end }}

    # Configure the physical settings of all peer ports named by the cell
{{- range .AllPorts }}
    config interface mtu {{ . }} 9100
    config interface fec {{ . }} rs
    config interface speed {{ . }} 100000
    config interface startup {{ . }}
{{- end }}

    # Configure the south-facing server ports: one VLAN per port with its
    # interface prefix and DHCP relay
{{- range .SouthPorts }}
    config vlan add {{ .VLAN }}
    config vlan member add {{ .VLAN }} {{ .Port }} -u
    config interface ip add Vlan{{ .VLAN }} {{ .IP }}
    config vlan dhcp_relay add {{ .VLAN }} {{ .Relay }}
{{- end }}

    # Enable IPv6 link-local
    config ipv6 enable link-local

    # Save running config
    config save -y

    # Update FRR (BGP) configuration
    cat <<EOF >/etc/sonic/frr/frr.conf
frr version 8.1
frr defaults traditional
hostname {{ .Hostname }}
log syslog informational
service integrated-vtysh-config
{{- range .SouthPorts }}

interface Vlan{{ .VLAN }}
  no ipv6 nd suppress-ra
  ipv6 nd managed-config-flag
  ipv6 nd other-config-flag
{{- end }}
router bgp {{ .ASN }}
  bgp router-id {{ .RouterID }}
  no bgp ebgp-requires-policy
  no bgp default ipv4-unicast
  bgp bestpath as-path multipath-relax
  no bgp network import-check

  neighbor FABRIC peer-group
  neighbor FABRIC remote-as external
  neighbor FABRIC timers 3 9
  neighbor FABRIC timers connect 20
{{- range .FabricPorts }}
  neighbor {{ . }} interface peer-group FABRIC
{{- end }}
{{- if .SouthPorts }}

  neighbor SOUTH peer-group
  neighbor SOUTH remote-as external
  neighbor SOUTH timers 3 9
  neighbor SOUTH timers connect 20
{{- range .SouthPorts }}
  neighbor Vlan{{ .VLAN }} interface peer-group SOUTH
{{- end }}
{{- end }}

  address-family ipv6 unicast
    network {{ .Summary }}

    neighbor FABRIC activate
    neighbor FABRIC route-map RM_FABRIC_IN in
    neighbor FABRIC route-map RM_FABRIC_OUT out
{{- if .SouthPorts }}

    neighbor SOUTH activate
    neighbor SOUTH route-map RM_SOUTH_IN in
    neighbor SOUTH route-map RM_SOUTH_OUT out
{{ else }}

{{ end }}  exit-address-family
exit

{{ if .IsLeaf -}}
route-map RM_FABRIC_IN permit 10
  set community 65000:100
!
bgp community-list 10 permit 65000:100

route-map RM_FABRIC_OUT deny 10
  match community 10
route-map RM_FABRIC_OUT permit 20
!
{{ else -}}
route-map RM_FABRIC_IN permit 10
route-map RM_FABRIC_OUT permit 10
!
{{ end -}}
{{ if .SouthPorts -}}
route-map RM_SOUTH_IN permit 10
route-map RM_SOUTH_OUT permit 10
!
{{ end -}}
ipv6 route {{ .Summary }} reject
EOF

    # Restart BGP to load new config
    systemctl restart bgp

    # Mark the cell as applied
    touch "$FLAG_FILE_DONE"
    reboot
fi
exit 0
