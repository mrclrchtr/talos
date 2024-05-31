// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package vip

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"go.uber.org/zap"

	"github.com/siderolabs/talos/pkg/download"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"
)

// HCloudHandler implements assignment and release of Virtual IPs using API.
type HCloudHandler struct {
	client *hcloud.Client

	logger *zap.Logger

	vip        string
	deviceID   int64
	floatingID int64
	networkID  int64
}

// NewHCloudHandler creates new NewEHCloudHandler.
func NewHCloudHandler(logger *zap.Logger, vip string, spec network.VIPHCloudSpec) *HCloudHandler {
	return &HCloudHandler{
		client: hcloud.NewClient(hcloud.WithToken(spec.APIToken)),

		logger: logger,

		vip:       vip,
		deviceID:  spec.DeviceID,
		networkID: spec.NetworkID,
	}
}

// Acquire implements Handler interface.
func (handler *HCloudHandler) Acquire(ctx context.Context) error {
	handler.logger.Warn("##################HCLOUD-DEBUG################## Acquire", zap.String("vip", handler.vip), zap.Int64("deviceID", handler.deviceID), zap.Int64("networkID", handler.networkID))
	if handler.networkID > 0 {
		handler.logger.Warn("##################HCLOUD-DEBUG################## networkID > 0", zap.String("vip", handler.vip))
		var action *hcloud.Action

		alias := hcloud.ServerChangeAliasIPsOpts{
			Network:  &hcloud.Network{ID: handler.networkID},
			AliasIPs: []net.IP{},
		}
		handler.logger.Warn("##################HCLOUD-DEBUG################## alias", zap.Any("alias", alias))

		// trying to find the old active server
		// and remove alias IP from it
		serverList, err := handler.client.Server.All(ctx)
		if err != nil {
			return fmt.Errorf("error getting server list: %w", err)
		}

		oldDeviceID := findServerByAlias(serverList, handler.networkID, handler.vip, handler.logger)
		handler.logger.Warn("##################HCLOUD-DEBUG################## oldDeviceID", zap.Int64("oldDeviceID", oldDeviceID))
		if oldDeviceID != 0 {
			action, _, err = handler.client.Server.ChangeAliasIPs(ctx,
				&hcloud.Server{ID: oldDeviceID},
				hcloud.ServerChangeAliasIPsOpts{
					Network:  &hcloud.Network{ID: handler.networkID},
					AliasIPs: []net.IP{},
				})
			if err != nil {
				return fmt.Errorf("error remove alias IPs %q on server %d: %w", handler.vip, oldDeviceID, err)
			}

			handler.logger.Info("cleared previous Hetzner Cloud IP alias", zap.String("vip", handler.vip),
				zap.Int64("device_id", oldDeviceID), zap.String("status", string(action.Status)))
		}

		netIP := net.ParseIP(handler.vip)
		alias.AliasIPs = []net.IP{netIP}

		action, _, err = handler.client.Server.ChangeAliasIPs(ctx,
			&hcloud.Server{ID: handler.deviceID},
			alias)
		if err != nil {
			return fmt.Errorf("error change alias IPs %q to server %d: %w", handler.vip, handler.deviceID, err)
		}

		handler.logger.Info("assigned Hetzner Cloud alias IP", zap.String("vip", handler.vip), zap.Int64("device_id", handler.deviceID),
			zap.Int64("network_id", handler.networkID), zap.String("status", string(action.Status)))

		return nil
	}

	floatips, err := handler.client.FloatingIP.All(ctx)
	handler.logger.Warn("##################HCLOUD-DEBUG################## floatips", zap.Any("floatips", floatips))
	if err != nil {
		return fmt.Errorf("error getting floatingIPs list: %w", err)
	}

	for _, floatip := range floatips {
		if floatip.IP.String() == handler.vip {
			handler.logger.Warn("##################HCLOUD-DEBUG################## floatip", zap.Any("floatip", floatip))
			action, _, err := handler.client.FloatingIP.Assign(ctx, floatip, &hcloud.Server{ID: handler.deviceID})
			if err != nil {
				return fmt.Errorf("error assigning %q on server %d: %w", handler.vip, handler.deviceID, err)
			}

			handler.logger.Info("assigned Hetzner Cloud floating IP", zap.String("vip", handler.vip), zap.Int64("device_id", handler.deviceID), zap.String("status", string(action.Status)))
			handler.floatingID = floatip.ID

			return nil
		}
	}

	return fmt.Errorf("error assigning %q to server %d: floating IP is not found", handler.vip, handler.deviceID)
}

// Release implements Handler interface.
func (handler *HCloudHandler) Release(ctx context.Context) error {
	handler.logger.Warn("##################HCLOUD-DEBUG################## Release", zap.String("vip", handler.vip))
	if handler.networkID > 0 {
		handler.logger.Warn("##################HCLOUD-DEBUG################## networkID > 0", zap.String("vip", handler.vip))
		alias := hcloud.ServerChangeAliasIPsOpts{
			Network:  &hcloud.Network{ID: handler.networkID},
			AliasIPs: []net.IP{},
		}

		action, _, err := handler.client.Server.ChangeAliasIPs(ctx,
			&hcloud.Server{ID: handler.deviceID},
			alias)
		if err != nil {
			return fmt.Errorf("error remove alias IPs %q on server %d: %w", handler.vip, handler.deviceID, err)
		}

		handler.logger.Info("unassigned Hetzner Cloud alias IP", zap.String("vip", handler.vip), zap.Int64("device_id", handler.deviceID),
			zap.Int64("network_id", handler.networkID), zap.String("status", string(action.Status)))

		return nil
	}

	if handler.floatingID > 0 {
		handler.logger.Warn("##################HCLOUD-DEBUG################## floatingID > 0", zap.String("vip", handler.vip))
		floatip, _, err := handler.client.FloatingIP.GetByID(ctx, handler.floatingID)
		if err != nil {
			return fmt.Errorf("error getting floatingIP info: %w", err)
		}

		if floatip.Server == nil || floatip.Server.ID != handler.deviceID {
			handler.logger.Info("unassigned Hetzner Cloud floating IP", zap.String("vip", handler.vip), zap.Int64("device_id", handler.deviceID))
		}

		handler.floatingID = 0
	}

	return nil
}

// HCloudMetaDataEndpoint is the local endpoint for machine info like networking.
const HCloudMetaDataEndpoint = "http://169.254.169.254/hetzner/v1/metadata/instance-id"

// GetNetworkAndDeviceIDs fills in parts of the spec based on the API token and instance metadata.
func GetNetworkAndDeviceIDs(ctx context.Context, spec *network.VIPHCloudSpec, vip netip.Addr, logger *zap.Logger) error {
	logger.Warn("##################HCLOUD-DEBUG################## GetNetworkAndDeviceIDs", zap.String("vip", vip.String()))
	metadataInstanceID, err := download.Download(ctx, HCloudMetaDataEndpoint)
	if err != nil {
		return fmt.Errorf("error downloading instance-id: %w", err)
	}

	spec.DeviceID, err = strconv.ParseInt(string(metadataInstanceID), 10, 64)
	if err != nil {
		return fmt.Errorf("error getting instance-id id: %w", err)
	}

	client := hcloud.NewClient(hcloud.WithToken(spec.APIToken))

	server, _, err := client.Server.GetByID(ctx, spec.DeviceID)
	if err != nil {
		return fmt.Errorf("error getting server info: %w", err)
	}

	spec.NetworkID = 0

	logger.Warn("##################HCLOUD-DEBUG################## private networks", zap.String("names", fmt.Sprintf("%v", server.PrivateNet)))
	for _, privnet := range server.PrivateNet {
		network, _, err := client.Network.GetByID(ctx, privnet.Network.ID)
		if err != nil {
			logger.Warn("##################HCLOUD-DEBUG################## error getting network info", zap.Error(err))
			return fmt.Errorf("error getting network info: %w", err)
		}
		logger.Warn("##################HCLOUD-DEBUG################## network", zap.String("network", network.Name), zap.String("ip-range", network.IPRange.String()), zap.String("vip", vip.String()))
		logger.Warn("##################HCLOUD-DEBUG################## IPRange contains VIP", zap.Bool("contains", network.IPRange.Contains(vip.AsSlice())))
		if network.IPRange.Contains(vip.AsSlice()) {
			logger.Warn("##################HCLOUD-DEBUG################## network found", zap.String("network", network.Name), zap.String("ip-range", network.IPRange.String()), zap.String("vip", vip.String()))
			spec.NetworkID = privnet.Network.ID
			logger.Warn("##################HCLOUD-DEBUG################## spec.NetworkID", zap.Int64("spec.NetworkID", spec.NetworkID))
			break
		}
	}

	return nil
}

func findServerByAlias(serverList []*hcloud.Server, networkID int64, vip string, logger *zap.Logger) (deviceID int64) {
	logger.Warn("##################HCLOUD-DEBUG################## findServerByAlias", zap.Int64("networkID", networkID), zap.String("vip", vip))
	for _, server := range serverList {
		logger.Warn("##################HCLOUD-DEBUG################## server", zap.Any("server", server))
		for _, network := range server.PrivateNet {
			logger.Warn("##################HCLOUD-DEBUG################## network", zap.Any("network", network))
			if network.Network.ID == networkID {
				logger.Warn("##################HCLOUD-DEBUG################## network found", zap.Any("network", network))
				for _, alias := range network.Aliases {
					logger.Warn("##################HCLOUD-DEBUG################## alias", zap.Any("alias", alias))
					if alias.String() == vip {
						logger.Warn("##################HCLOUD-DEBUG################## alias == vip", zap.Any("alias", alias), zap.String("vip", vip))
						return server.ID
					}
				}
			}
		}
	}

	logger.Warn("##################HCLOUD-DEBUG################## deviceID not found")
	return 0
}
