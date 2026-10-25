// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package configdb

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	applDBIndex   = 0
	configDBIndex = 4
	adminDown     = "down"
)

type PortConfig struct {
	MTU     int
	FEC     string
	Speed   int
	AdminUp bool
}

type VLANConfig struct {
	ID        int
	Prefix    string
	Port      string
	DHCPRelay string
}

type DB interface {
	SetHostname(ctx context.Context, hostname string) error
	EnsureLoopback(ctx context.Context, name string, prefixes []string) error
	EnsurePort(ctx context.Context, name string, cfg PortConfig) error
	EnsureVLAN(ctx context.Context, cfg VLANConfig) error
	EnableLinkLocal(ctx context.Context, interfaceName string) error
	InterfaceOperUp(ctx context.Context, interfaceName string) (bool, error)
	SetInterfaceAdminState(ctx context.Context, interfaceName string, up bool) error
	Reset(ctx context.Context) error
	Close() error
}

type Client struct {
	config *redis.Client
	appl   *redis.Client
}

var _ DB = (*Client)(nil)

func New(address string) DB {
	newClient := func(db int) *redis.Client {
		return redis.NewClient(&redis.Options{
			Addr:             address,
			DB:               db,
			DialTimeout:      30 * time.Second,
			ReadTimeout:      5 * time.Second,
			WriteTimeout:     5 * time.Second,
			PoolTimeout:      10 * time.Second,
			MaxRetries:       10,
			MinRetryBackoff:  500 * time.Millisecond,
			MaxRetryBackoff:  10 * time.Second,
			DisableIndentity: true,
		})
	}
	return &Client{config: newClient(configDBIndex), appl: newClient(applDBIndex)}
}

func (c *Client) Close() error {
	return errors.Join(c.config.Close(), c.appl.Close())
}

func (c *Client) SetHostname(ctx context.Context, hostname string) error {
	if hostname == "" {
		return fmt.Errorf("hostname must not be empty")
	}
	if err := c.config.HSet(ctx, "DEVICE_METADATA|localhost", "hostname", hostname).Err(); err != nil {
		return fmt.Errorf("setting hostname: %w", err)
	}
	return nil
}

func (c *Client) EnsureLoopback(ctx context.Context, name string, prefixes []string) error {
	if !strings.HasPrefix(name, "Loopback") {
		return fmt.Errorf("invalid loopback name %q", name)
	}
	if err := c.config.HSet(ctx, "LOOPBACK_INTERFACE|"+name, "NULL", "NULL").Err(); err != nil {
		return fmt.Errorf("ensuring loopback %s: %w", name, err)
	}
	return c.syncIPAddresses(ctx, "LOOPBACK_INTERFACE", name, prefixes)
}

func (c *Client) EnsurePort(ctx context.Context, name string, cfg PortConfig) error {
	if err := validatePortName(name); err != nil {
		return err
	}
	if cfg.MTU < 68 || cfg.MTU > 9216 {
		return fmt.Errorf("invalid MTU %d for %s", cfg.MTU, name)
	}
	if cfg.FEC != "rs" && cfg.FEC != "fc" && cfg.FEC != "none" {
		return fmt.Errorf("invalid FEC %q for %s", cfg.FEC, name)
	}
	if cfg.Speed <= 0 {
		return fmt.Errorf("invalid speed %d for %s", cfg.Speed, name)
	}
	adminStatus := adminDown
	if cfg.AdminUp {
		adminStatus = "up"
	}
	if err := c.config.HSet(ctx, "PORT|"+name,
		"mtu", strconv.Itoa(cfg.MTU),
		"fec", cfg.FEC,
		"speed", strconv.Itoa(cfg.Speed),
		"admin_status", adminStatus,
	).Err(); err != nil {
		return fmt.Errorf("configuring port %s: %w", name, err)
	}
	return nil
}

func (c *Client) EnsureVLAN(ctx context.Context, cfg VLANConfig) error {
	if cfg.ID < 1 || cfg.ID > 4094 {
		return fmt.Errorf("invalid VLAN ID %d", cfg.ID)
	}
	if err := validatePortName(cfg.Port); err != nil {
		return err
	}
	if _, err := netip.ParsePrefix(cfg.Prefix); err != nil {
		return fmt.Errorf("invalid VLAN prefix %q: %w", cfg.Prefix, err)
	}
	if _, err := netip.ParseAddr(cfg.DHCPRelay); err != nil {
		return fmt.Errorf("invalid DHCP relay %q: %w", cfg.DHCPRelay, err)
	}

	vlanName := "Vlan" + strconv.Itoa(cfg.ID)
	if err := c.config.HSet(ctx, "VLAN|"+vlanName, "vlanid", strconv.Itoa(cfg.ID)).Err(); err != nil {
		return fmt.Errorf("ensuring VLAN %s: %w", vlanName, err)
	}
	if err := c.config.HSet(ctx, "VLAN_MEMBER|"+vlanName+"|"+cfg.Port, "tagging_mode", "untagged").Err(); err != nil {
		return fmt.Errorf("ensuring VLAN member %s on %s: %w", cfg.Port, vlanName, err)
	}
	if err := c.config.HSet(ctx, "VLAN_INTERFACE|"+vlanName, "NULL", "NULL").Err(); err != nil {
		return fmt.Errorf("ensuring VLAN interface %s: %w", vlanName, err)
	}
	if err := c.syncIPAddresses(ctx, "VLAN_INTERFACE", vlanName, []string{cfg.Prefix}); err != nil {
		return err
	}
	if err := c.config.HSet(ctx, "VLAN|"+vlanName, "dhcp_servers@", cfg.DHCPRelay).Err(); err != nil {
		return fmt.Errorf("configuring DHCP relay on %s: %w", vlanName, err)
	}
	return nil
}

func (c *Client) EnableLinkLocal(ctx context.Context, interfaceName string) error {
	var table string
	switch {
	case strings.HasPrefix(interfaceName, "Ethernet"):
		if err := validatePortName(interfaceName); err != nil {
			return err
		}
		table = "INTERFACE"
	case strings.HasPrefix(interfaceName, "Vlan"):
		if err := validateVLANName(interfaceName); err != nil {
			return err
		}
		table = "VLAN_INTERFACE"
	default:
		return fmt.Errorf("unsupported link-local interface %q", interfaceName)
	}
	if err := c.config.HSet(ctx, table+"|"+interfaceName, "ipv6_use_link_local_only", "enable").Err(); err != nil {
		return fmt.Errorf("enabling IPv6 link-local on %s: %w", interfaceName, err)
	}
	return nil
}

func (c *Client) InterfaceOperUp(ctx context.Context, interfaceName string) (bool, error) {
	if err := validatePortName(interfaceName); err != nil {
		return false, err
	}
	value, err := c.appl.HGet(ctx, "PORT_TABLE:"+interfaceName, "oper_status").Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return false, nil
		}
		return false, fmt.Errorf("reading operational state of %s: %w", interfaceName, err)
	}
	return value == "up", nil
}

func (c *Client) SetInterfaceAdminState(ctx context.Context, interfaceName string, up bool) error {
	if err := validatePortName(interfaceName); err != nil {
		return err
	}
	value := adminDown
	if up {
		value = "up"
	}
	if err := c.config.HSet(ctx, "PORT|"+interfaceName, "admin_status", value).Err(); err != nil {
		return fmt.Errorf("setting admin state of %s: %w", interfaceName, err)
	}
	return nil
}

func (c *Client) Reset(ctx context.Context) error {
	if err := c.deleteMatching(ctx, "LOOPBACK_INTERFACE|*|*"); err != nil {
		return err
	}

	vlans, err := scanKeys(ctx, c.config, "VLAN|*")
	if err != nil {
		return fmt.Errorf("listing ConfigDB VLANs: %w", err)
	}
	for _, vlan := range vlans {
		if err := c.config.HDel(ctx, vlan, "dhcp_servers@").Err(); err != nil {
			return fmt.Errorf("removing DHCP relay from %s: %w", strings.TrimPrefix(vlan, "VLAN|"), err)
		}
	}
	for _, pattern := range []string{"VLAN_INTERFACE|*", "VLAN_MEMBER|*", "VLAN|*"} {
		if err := c.deleteMatching(ctx, pattern); err != nil {
			return err
		}
	}

	ports, err := scanKeys(ctx, c.config, "PORT|*")
	if err != nil {
		return fmt.Errorf("listing ConfigDB ports: %w", err)
	}
	for _, port := range ports {
		if err := c.config.HSet(ctx, port, "admin_status", adminDown).Err(); err != nil {
			return fmt.Errorf("disabling %s: %w", strings.TrimPrefix(port, "PORT|"), err)
		}
	}
	return nil
}

func (c *Client) deleteMatching(ctx context.Context, pattern string) error {
	keys, err := scanKeys(ctx, c.config, pattern)
	if err != nil {
		return fmt.Errorf("listing ConfigDB keys matching %s: %w", pattern, err)
	}
	if len(keys) == 0 {
		return nil
	}
	if err := c.config.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("removing ConfigDB keys matching %s: %w", pattern, err)
	}
	return nil
}

func (c *Client) syncIPAddresses(ctx context.Context, table, interfaceName string, desired []string) error {
	desiredSet := make(map[string]struct{}, len(desired))
	for _, prefix := range desired {
		if _, err := netip.ParsePrefix(prefix); err != nil {
			return fmt.Errorf("invalid IP prefix %q: %w", prefix, err)
		}
		desiredSet[prefix] = struct{}{}
	}

	keyPrefix := table + "|" + interfaceName + "|"
	keys, err := scanKeys(ctx, c.config, keyPrefix+"*")
	if err != nil {
		return fmt.Errorf("listing IP addresses for %s: %w", interfaceName, err)
	}
	for _, key := range keys {
		if _, ok := desiredSet[strings.TrimPrefix(key, keyPrefix)]; ok {
			continue
		}
		if err := c.config.Del(ctx, key).Err(); err != nil {
			return fmt.Errorf("removing stale IP address %s: %w", key, err)
		}
	}
	for _, prefix := range desired {
		if err := c.config.HSet(ctx, keyPrefix+prefix, "NULL", "NULL").Err(); err != nil {
			return fmt.Errorf("adding IP address %s to %s: %w", prefix, interfaceName, err)
		}
	}
	return nil
}

func scanKeys(ctx context.Context, client *redis.Client, pattern string) ([]string, error) {
	var keys []string
	var cursor uint64
	for {
		batch, next, err := client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, err
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 {
			return keys, nil
		}
	}
}

func validatePortName(name string) error {
	if !strings.HasPrefix(name, "Ethernet") {
		return fmt.Errorf("invalid port name %q", name)
	}
	if _, err := strconv.Atoi(strings.TrimPrefix(name, "Ethernet")); err != nil {
		return fmt.Errorf("invalid port name %q", name)
	}
	return nil
}

func validateVLANName(name string) error {
	if !strings.HasPrefix(name, "Vlan") {
		return fmt.Errorf("invalid VLAN name %q", name)
	}
	id, err := strconv.Atoi(strings.TrimPrefix(name, "Vlan"))
	if err != nil || id < 1 || id > 4094 {
		return fmt.Errorf("invalid VLAN name %q", name)
	}
	return nil
}
