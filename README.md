# Packer Plugin for Oracle Linux Virtualization Manager (OLVM)

This repository contains a Packer plugin for building OLVM images. The plugin allows you to create a customized template from either a source template, or a source disk image. This may include, for example, official Oracle Linux OVA templates provided by Oracle, or cloud disk images provided by many Linux distributions (Ubuntu, RockyLinux, etc.)

## Features

- Creates a Packer build VM from an OLVM template or disk image
- Configures SSH access and networking, and supports static IPs
- Generate and optionally export template artifacts (in OVA format) for distribution
- Uses cloud-init for network and SSH authorized keys configuration
- Automatic VM cleanup and template creation
- Support for both template-based and disk-based source images

## Installation

### Prerequisites

- Packer 1.7.0 or later
- Access to an OLVM/oVirt environment
- Network connectivity to the OLVM API

### Plugin Installation

1. Download the latest release for your platform from the [releases page](https://github.com/mikelaws/packer-plugin-olvm/releases)
2. Extract the binary to your Packer plugin directory:
   ```bash
   mkdir -p ~/.packer.d/plugins
   cp packer-builder-olvm ~/.packer.d/plugins/
   chmod +x ~/.packer.d/plugins/packer-builder-olvm
   ```

### Building from Source

```bash
git clone https://github.com/mikelaws/packer-plugin-olvm.git
cd packer-plugin-olvm
go build -o bin/packer-builder-olvm ./builder/olvm
```

## Configuration

The OLVM builder supports the following configuration options:

### Required Configuration

#### OLVM Configuration

- `olvm_url` - The URL of the OLVM API endpoint
- `username` - Username for OLVM authentication
- `password` - Password for OLVM authentication

#### Source Configuration (any one of the following)

- `source_template_name` - Name of the source template
- `source_template_id` - ID of the source template (alternative to source_template_name)
- `source_disk_name` - Name of the source disk image
- `source_disk_id` - ID of the source disk image (alternative to source_disk_name)

### Optional Configuration

#### Source Configuration

- `source_template_version` - Version of the source template (defaults to 1)
- `cluster` - OLVM cluster name (defaults to "Default")

#### VM Configuration

- `vm_name` - Name for the VM (defaults to "packer-<time-ordered-uuid>")
- `vm_vcpu_count` - Number of virtual CPUs
- `vm_memory_mb` - Memory in MB
- `vm_storage_driver` - Storage interface type (defaults to "virtio-scsi")

#### Network Configuration

- `network_name` - Name of the OLVM network to attach to the VM
- `vnic_profile` - vNIC profile to use for the network interface (defaults to `network_name` if not specified)
- `dns_servers` - List of DNS server IP addresses
- `os_interface_name` - Operating system network interface name (defaults to "eth0")
- `address` - Static IP address for the VM
- `netmask` - Network mask (defaults to "255.255.255.0")
- `gateway` - Gateway address

#### Template Creation

- `destination_template_name` - Name for the generated template (optional)
- `destination_template_description` - Description for the template. Defaults to "Template created by Packer from VM <vm_name>".

#### Cleanup Configuration

- `cleanup_vm` - Whether to delete the VM after template creation (defaults to true)
- `cleanup_interfaces` - Whether to remove network interfaces before template creation (defaults to true)

#### Export Configuration

- `export_host` - Host to export the template to
- `export_directory` - Directory on the export host to save the template (defaults to "/tmp")
- `export_file_name` - Filename for the exported OVA file (defaults to "<destination_template_name>.ova")

#### SSH Configuration

- `ssh_username` - SSH username
- `ssh_timeout` - SSH connection timeout (defaults to 5m)
- `ssh_handshake_attempts` - Number of SSH handshake attempts (defaults to 10)

#### TLS Configuration

- `tls_insecure` - Skip TLS verification (defaults to false)

## Example Usage

### Template-based Build

```hcl
source "olvm" "template-example" {
  # OLVM Configuration
  olvm_url = "https://olvm.example.com/ovirt-engine/api"
  username = "admin@internal"
  password = "password"
  tls_insecure = true

  # Source Configuration
  source_template_name = "oracle-linux-8-template"
  cluster = "Default"

  # Network Configuration
  network_name = "ovirtmgmt"
  vnic_profile = "ovirtmgmt"
  dns_servers   = ["8.8.8.8", "8.8.4.4"]
  os_interface_name = "ens3"  # For systems using predictable network interface names
  
  # VM Configuration
  vm_name       = "packer-test-vm"
  vm_vcpu_count = 2
  vm_memory_mb  = 4096
  
  # SSH Configuration
  ssh_username = "root"
  ssh_timeout  = "30m"
  
  # Template Configuration
  destination_template_name        = "my-custom-template"
  destination_template_description = "Template created by Packer"
  
  # Cleanup Configuration
  cleanup_vm         = true
  cleanup_interfaces = true
  
  # Export Configuration (optional)
  export_host      = "export.example.com"
  export_directory = "/exports"
  export_file_name = "my-template.ova"
}

build {
  sources = ["source.olvm.template-example"]
}
```

### Disk-based Build

```hcl
source "olvm" "disk-example" {
  # OLVM Configuration
  olvm_url = "https://olvm.example.com/ovirt-engine/api"
  username = "admin@internal"
  password = "password"
  tls_insecure = true

  # Source Configuration
  source_disk_name = "ubuntu-22.04-cloud-disk"
  cluster = "Default"

  # Network Configuration
  network_name = "ovirtmgmt"
  vnic_profile = "ovirtmgmt"
  dns_servers   = ["8.8.8.8", "8.8.4.4"]
  os_interface_name = "ens3"
  
  # VM Configuration
  vm_name       = "packer-ubuntu-vm"
  vm_vcpu_count = 2
  vm_memory_mb  = 4096
  vm_storage_driver = "virtio-scsi"
  
  # SSH Configuration
  ssh_username = "ubuntu"
  ssh_timeout  = "30m"
  
  # Template Configuration
  destination_template_name = "ubuntu-22.04-template"
  
  # Cleanup Configuration
  cleanup_vm         = true
  cleanup_interfaces = true
}

build {
  sources = ["source.olvm.disk-example"]
}
```

## Building

To build the plugin:

```bash
make build
```

Or manually:

```bash
go build -o bin/packer-builder-olvm ./builder/olvm
```

## Development

### Prerequisites

- Go 1.19 or later
- Packer 1.7.0 or later

### Running Tests

```bash
go test ./...
```

### Code Generation

The HCL2 specification is auto-generated. To regenerate it:

```bash
go generate ./builder/olvm
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests for new functionality
5. Ensure all tests pass
6. Submit a pull request

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Support

For issues and questions, please use the [GitHub issue tracker](https://github.com/mikelaws/packer-plugin-olvm/issues).
