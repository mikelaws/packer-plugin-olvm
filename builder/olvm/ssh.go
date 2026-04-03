package olvm

import (
	"fmt"
	"log"
	"time"

	"github.com/hashicorp/packer-plugin-sdk/multistep"
	ovirtsdk4 "github.com/ovirt/go-ovirt"
)

func commHost(state multistep.StateBag) (string, error) {
	c := state.Get("config").(*Config)

	// If a static IP was configured, use it directly.
	if c.IPAddress != "" {
		log.Printf("Using configured static IP address for SSH: %s", c.IPAddress)
		return c.IPAddress, nil
	}

	// No static IP — query OLVM for the IP reported by the guest agent (DHCP case).
	vmID, ok := state.GetOk("vm_id")
	if !ok {
		return "", fmt.Errorf("vm_id not found in state; cannot determine SSH host")
	}
	connWrapper, ok := state.Get("connWrapper").(*ConnectionWrapper)
	if !ok {
		return "", fmt.Errorf("connWrapper not found in state; cannot determine SSH host")
	}

	log.Printf("No static IP configured; polling OLVM for VM %s reported IP address...", vmID.(string))

	timeout := time.After(5 * time.Minute)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return "", fmt.Errorf("timed out waiting for VM %s to report an IP address via guest agent", vmID.(string))
		case <-ticker.C:
			ip, err := getVMReportedIP(connWrapper, vmID.(string))
			if err != nil {
				log.Printf("Error querying VM reported devices: %s; retrying...", err)
				continue
			}
			if ip != "" {
				log.Printf("VM %s reported IP address: %s", vmID.(string), ip)
				return ip, nil
			}
			log.Printf("VM %s has not reported an IP address yet; retrying...", vmID.(string))
		}
	}
}

// getVMReportedIP queries OLVM's reported devices for the VM's first non-loopback
// IPv4 address (falls back to IPv6 if no IPv4 is available).
func getVMReportedIP(connWrapper *ConnectionWrapper, vmID string) (string, error) {
	var found string
	err := connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
		resp, err := conn.SystemService().
			VmsService().
			VmService(vmID).
			ReportedDevicesService().
			List().
			Send()
		if err != nil {
			return err
		}
		devices, ok := resp.ReportedDevice()
		if !ok {
			return nil
		}
		for _, dev := range devices.Slice() {
			ips, ok := dev.Ips()
			if !ok {
				continue
			}
			for _, ip := range ips.Slice() {
				addr, ok := ip.Address()
				if !ok || addr == "" || addr == "127.0.0.1" || addr == "::1" {
					continue
				}
				version, ok := ip.Version()
				if ok && version == ovirtsdk4.IPVERSION_V4 {
					found = addr
					return nil // prefer IPv4; stop on first match
				}
				if found == "" {
					found = addr // keep as IPv6 fallback
				}
			}
		}
		return nil
	})
	return found, err
}
