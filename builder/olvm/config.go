package olvm

import (
	"errors"
	"fmt"
	"log"

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

	Cluster                        string   `mapstructure:"cluster"`
	VMName                         string   `mapstructure:"vm_name"`
	VmVcpuCount                    int      `mapstructure:"vm_vcpu_count"`
	VmMemoryMB                     int      `mapstructure:"vm_memory_mb"`
	IPAddress                      string   `mapstructure:"address"`
	Netmask                        string   `mapstructure:"netmask"`
	Gateway                        string   `mapstructure:"gateway"`
	NetworkName                    string   `mapstructure:"network_name"`
	VnicProfile                    string   `mapstructure:"vnic_profile"`
	DNSServers                     []string `mapstructure:"dns_servers"`
	DestinationTemplateName        string   `mapstructure:"destination_template_name"`
	DestinationTemplateDescription string   `mapstructure:"destination_template_description"`
	CleanupInterfaces              bool     `mapstructure:"cleanup_interfaces"`
	CleanupVM                      bool     `mapstructure:"cleanup_vm"`
	ExportHost                     string   `mapstructure:"export_host"`
	ExportDirectory                string   `mapstructure:"export_directory"`
	ExportFileName                 string   `mapstructure:"export_file_name"`

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

	// Validate export configuration
	if (c.ExportDirectory != "" || c.ExportFileName != "") && c.ExportHost == "" {
		errs = packer.MultiErrorAppend(errs, errors.New("export_host must be specified when export_directory or export_file_name are set"))
	}

	// Set default export directory if export_host is specified
	if c.ExportHost != "" && c.ExportDirectory == "" {
		c.ExportDirectory = "/tmp"
		log.Printf("Using default export_directory: %s", c.ExportDirectory)
	}

	errs = packer.MultiErrorAppend(errs, c.Comm.Prepare(&c.ctx)...)

	if errs != nil && len(errs.Errors) > 0 {
		return nil, nil, errs
	}

	packer.LogSecretFilter.Set(c.Password)
	return c, nil, nil
}
