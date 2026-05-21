// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controller

type InitDeviceInterfaceOptions struct {
	Name   string
	Handle string
}

type InitOptions struct {
	Interfaces []InitDeviceInterfaceOptions
}
