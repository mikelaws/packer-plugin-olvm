package olvm

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/hashicorp/packer-plugin-sdk/common"
	"github.com/hashicorp/packer-plugin-sdk/communicator"
	"github.com/hashicorp/packer-plugin-sdk/packer"
	"github.com/hashicorp/packer-plugin-sdk/template/config"
	"github.com/hashicorp/packer-plugin-sdk/template/interpolate"
	"github.com/hashicorp/packer-plugin-sdk/uuid"
)

type Config struct {
	common.PackerConfig `mapstructure:",squash"`

	AccessConfig `mapstructure:",squash"`
	SourceConfig `mapstructure:",squash"`

	Comm communicator.Config `mapstructure:",squash"`

	VMName                         string   `mapstructure:"vm_name"`
	VmVcpuCount                    int      `mapstructure:"vm_vcpu_count"`
	VmMemoryMB                     int      `mapstructure:"vm_memory_mb"`
	VMStorageDriver                string   `mapstructure:"vm_storage_driver"`
	VMFirmwareType                 string   `mapstructure:"vm_firmware_type"`
	IPAddress                      string   `mapstructure:"address"`
	Netmask                        string   `mapstructure:"netmask"`
	Gateway                        string   `mapstructure:"gateway"`
	NetworkName                    string   `mapstructure:"network_name"`
	VnicProfile                    string   `mapstructure:"vnic_profile"`
	DNSServers                     []string `mapstructure:"dns_servers"`
	OSInterfaceName                string   `mapstructure:"os_interface_name"`
	DestinationTemplateName        string   `mapstructure:"destination_template_name"`
	DestinationTemplateDescription string   `mapstructure:"destination_template_description"`
	CleanupInterfaces              *bool    `mapstructure:"cleanup_interfaces"`
	CleanupVM                      *bool    `mapstructure:"cleanup_vm"`
	ExportHost                     string   `mapstructure:"export_host"`
	ExportDirectory                string   `mapstructure:"export_directory"`
	ExportFileName                 string   `mapstructure:"export_file_name"`
	MaxRetries                     int      `mapstructure:"max_retries"`
	RetryIntervalSec               int      `mapstructure:"retry_interval_sec"`
	TemplateSeal                   *bool    `mapstructure:"template_seal"`
	TemplateResetCloudInit         *bool    `mapstructure:"template_reset_cloud_init"`
	TemplateHighAvailability       bool     `mapstructure:"template_high_availability"`
	TemplateHALeaseStorageDomain   string   `mapstructure:"template_ha_lease_storage_domain"`
	TemplateHAResumeBehavior       string   `mapstructure:"template_ha_resume_behavior"`

	ctx interpolate.Context
}

func NewConfig(raws ...interface{}) (*Config, []string, error) {
	c := new(Config)

	err := config.Decode(c, &config.DecodeOpts{
		Interpolate:        true,
		InterpolateContext: &c.ctx,
	}, raws...)
	if err != nil {
		return nil, nil, err
	}

	// Accumulate any errors
	var errs *packer.MultiError
	errs = packer.MultiErrorAppend(errs, c.AccessConfig.Prepare(&c.ctx)...)
	errs = packer.MultiErrorAppend(errs, c.SourceConfig.Prepare(&c.ctx)...)

	if c.VMName == "" {
		// Default to packer-[time-ordered-uuid]
		c.VMName = fmt.Sprintf("packer-%s", uuid.TimeOrderedUUID())
	}
	if c.Netmask == "" {
		c.Netmask = "255.255.255.0"
		log.Printf("Set default netmask to %s", c.Netmask)
	}

	// Set default value for vm_storage_driver if not specified
	if c.VMStorageDriver == "" {
		c.VMStorageDriver = "virtio-scsi"
		log.Printf("Using default vm_storage_driver: %s", c.VMStorageDriver)
	}

	// Validate vm_storage_driver value
	validStorageDrivers := []string{"virtio-scsi", "virtio"}
	validDriver := false
	for _, driver := range validStorageDrivers {
		if c.VMStorageDriver == driver {
			validDriver = true
			break
		}
	}
	if !validDriver {
		errs = packer.MultiErrorAppend(errs, fmt.Errorf("Invalid vm_storage_driver: %s. Must be one of: %v", c.VMStorageDriver, validStorageDrivers))
	}

	// Validate vm_firmware_type if provided
	if c.VMFirmwareType != "" {
		validFirmwareTypes := []string{"bios", "uefi"}
		validFirmware := false
		for _, fwType := range validFirmwareTypes {
			if strings.ToLower(c.VMFirmwareType) == fwType {
				validFirmware = true
				c.VMFirmwareType = strings.ToLower(c.VMFirmwareType)
				break
			}
		}
		if !validFirmware {
			errs = packer.MultiErrorAppend(errs, fmt.Errorf("Invalid vm_firmware_type: %s. Must be one of: %v (or leave unset to use cluster default)", c.VMFirmwareType, validFirmwareTypes))
		}
	}

	// Validate export configuration
	if (c.ExportDirectory != "" || c.ExportFileName != "") && c.ExportHost == "" {
		errs = packer.MultiErrorAppend(errs, errors.New("export_host must be specified when export_directory or export_file_name are set"))
	}

	// Set default export directory if export_host is specified
	if c.ExportHost != "" && c.ExportDirectory == "" {
		c.ExportDirectory = "/tmp"
		log.Printf("Using default export_directory: %s", c.ExportDirectory)
	}

	// Set default value for os_interface_name if not specified
	if c.OSInterfaceName == "" {
		c.OSInterfaceName = "eth0"
		log.Printf("Using default os_interface_name: %s", c.OSInterfaceName)
	}

	// Set default values for VM resources if not specified
	if c.VmVcpuCount == 0 {
		c.VmVcpuCount = 1
		log.Printf("Using default vm_vcpu_count: %d", c.VmVcpuCount)
	}
	if c.VmMemoryMB == 0 {
		c.VmMemoryMB = 1024
		log.Printf("Using default vm_memory_mb: %d", c.VmMemoryMB)
	}

	// Set default value for network_name if not specified
	if c.NetworkName == "" {
		c.NetworkName = "ovirtmgmt"
		log.Printf("Using default network_name: %s", c.NetworkName)
	}

	// Set default values for retry configuration
	if c.MaxRetries == 0 {
		c.MaxRetries = 4
		log.Printf("Using default max_retries: %d", c.MaxRetries)
	}
	if c.RetryIntervalSec == 0 {
		c.RetryIntervalSec = 2
		log.Printf("Using default retry_interval_sec: %d", c.RetryIntervalSec)
	}

	// Set default value for template_seal if not specified
	if c.TemplateSeal == nil {
		defaultSeal := true
		c.TemplateSeal = &defaultSeal
		log.Printf("Using default template_seal: %t", *c.TemplateSeal)
	} else {
		log.Printf("Using configured template_seal: %t", *c.TemplateSeal)
	}

	// Set default for template_reset_cloud_init (default true = reset so not inherited)
	if c.TemplateResetCloudInit == nil {
		defaultReset := true
		c.TemplateResetCloudInit = &defaultReset
		log.Printf("Using default template_reset_cloud_init: %t", *c.TemplateResetCloudInit)
	} else {
		log.Printf("Using configured template_reset_cloud_init: %t", *c.TemplateResetCloudInit)
	}

	// Set default for template_ha_resume_behavior when HA is enabled
	if c.TemplateHighAvailability && c.TemplateHAResumeBehavior == "" {
		c.TemplateHAResumeBehavior = "auto_resume"
		log.Printf("Using default template_ha_resume_behavior: %s", c.TemplateHAResumeBehavior)
	}
	if c.TemplateHAResumeBehavior != "" {
		validResume := []string{"auto_resume", "kill", "leave_paused"}
		valid := false
		for _, r := range validResume {
			if strings.ToLower(c.TemplateHAResumeBehavior) == r {
				valid = true
				c.TemplateHAResumeBehavior = strings.ToLower(c.TemplateHAResumeBehavior)
				break
			}
		}
		if !valid {
			errs = packer.MultiErrorAppend(errs, fmt.Errorf("Invalid template_ha_resume_behavior: %s. Must be one of: %v", c.TemplateHAResumeBehavior, validResume))
		}
	}

	errs = packer.MultiErrorAppend(errs, c.Comm.Prepare(&c.ctx)...)

	// Handle SSH timeout after communicator preparation to prevent override
	// The communicator's Prepare method may override SSHTimeout with SSHWaitTimeout
	if c.Comm.SSHTimeout == 0 {
		c.Comm.SSHTimeout = 5 * time.Minute
		log.Printf("Set default ssh_timeout: %s", c.Comm.SSHTimeout)
	} else {
		log.Printf("Using configured ssh_timeout: %s", c.Comm.SSHTimeout)
	}

	// Clear SSHWaitTimeout to prevent it from overriding SSHTimeout
	if c.Comm.SSHWaitTimeout != 0 {
		log.Printf("Warning: ssh_wait_timeout was set to %s, clearing to prevent override of ssh_timeout", c.Comm.SSHWaitTimeout)
		c.Comm.SSHWaitTimeout = 0
	}

	if errs != nil && len(errs.Errors) > 0 {
		return nil, nil, errs
	}

	packer.LogSecretFilter.Set(c.Password)
	return c, nil, nil
}
