# OLVM Builder Configuration Reference

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
  network_name = "ovirtmgmt"  # Optional (defaults to "ovirtmgmt")
  vnic_profile = "ovirtmgmt"
  dns_servers = ["8.8.8.8", "8.8.4.4"]
  os_interface_name = "ens3"  # For systems using predictable network interface names

  # VM Configuration
  vm_name = "packer-test-vm"
  # vm_vcpu_count and vm_memory_mb are optional (default to 1 CPU, 1024MB)
  vm_vcpu_count = 2
  vm_memory_mb = 4096

  # SSH Configuration
  ssh_username = "root"
  ssh_timeout = "30m"

  # Template Configuration
  destination_template_name = "my-custom-template"
  destination_template_description = "Template created by Packer"

  # Cleanup Configuration
  cleanup_vm = true
  cleanup_interfaces = true
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
  network_name = "ovirtmgmt"  # Optional (defaults to "ovirtmgmt")
  vnic_profile = "ovirtmgmt"
  dns_servers = ["8.8.8.8", "8.8.4.4"]
  os_interface_name = "ens3"

  # VM Configuration
  vm_name = "packer-ubuntu-vm"
  # vm_vcpu_count and vm_memory_mb are optional (default to 1 CPU, 1024MB)
  vm_vcpu_count = 2
  vm_memory_mb = 4096
  vm_storage_driver = "virtio-scsi"

  # SSH Configuration
  ssh_username = "ubuntu"
  ssh_timeout = "30m"

  # Template Configuration
  destination_template_name = "ubuntu-22.04-template"

  # Cleanup Configuration
  cleanup_vm = true
  cleanup_interfaces = true
}

build {
  sources = ["source.olvm.disk-example"]
}
```

## Configuration Options

The OLVM builder supports the following parameters:

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
- `vm_vcpu_count` - Number of virtual CPUs (defaults to 1)
- `vm_memory_mb` - Memory in MB (defaults to 1024)
- `vm_storage_driver` - Storage interface type (defaults to "virtio-scsi")

#### Network Configuration

- `network_name` - Name of the OLVM network to attach to the VM (defaults to "ovirtmgmt")
- `vnic_profile` - vNIC profile to use for the network interface (defaults to `network_name` if not specified)
- `dns_servers` - List of DNS server IP addresses
- `os_interface_name` - Operating system network interface name (defaults to "eth0")
- `address` - Static IP address for the VM
- `netmask` - Network mask (defaults to "255.255.255.0")
- `gateway` - Gateway address

> **Note:** For template-based builds, if the source template already has network interfaces configured, the plugin will configure the first existing interface with the specified `network_name` and `vnic_profile`. If no network interfaces exist, a new one will be created. For disk-based builds, a new network interface is always created.

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

## Environment Variables

The following environment variables can be used instead of configuration options:

- `OLVM_URL` - OLVM API URL
- `OLVM_USERNAME` - OLVM username
- `OLVM_PASSWORD` - OLVM password

## Packer Compatibility

This plugin is compatible with Packer >= v1.10.2
