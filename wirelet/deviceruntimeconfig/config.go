// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package deviceruntimeconfig

type Config struct {
	Name       string `json:"name"`
	ProviderID string `json:"providerID"`
}

type Interface struct {
	Name string `json:"name"`
}
