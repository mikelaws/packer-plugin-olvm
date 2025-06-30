# Packer Plugin for Oracle Linux Virtualization Manager (OLVM)

This repository contains a Packer plugin for building OLVM images. The plugin allows you to create a customized template from a source template in an OLVM environment.

## Features

- Creates a Packer build VM from and OLVM template
- Configures SSH access and networking, and supports static IPs
- Generate and optionally export template artifacts for deployment
- Uses cloud-init for network and SSH authorized keys configuration
- Automatic VM cleanup and template creation

## Components

This plugin contains:
- OLVM Builder ([builder/olvm](builder/olvm)) - Creates and OLVM template from a source template
- Documentation ([docs](docs))
- Examples ([example](example))

## Configuration

The OLVM builder supports the following configuration options:

### Required Configuration

- `olvm_url` - The URL of the OLVM API endpoint
- `username` - Username for OLVM authentication
- `password` - Password for OLVM authentication
- `source_template_name` or `source_template_id` - Template to use as base

### Optional Configuration

#### Connection and Authentication
- `tls_insecure` - Skip TLS certificate validation (defaults to false)

#### Source Configuration
- `source_type` - Type of source (defaults to "template")
- `source_template_name` - Name of the source template
- `source_template_version` - Version of the source template (defaults to 1)
- `source_template_id` - ID of the source template (alternative to source_template_name)

#### VM Configuration
- `cluster` - Target cluster (defaults to "Default")
- `vm_name` - Name for the created VM (auto-generated if not specified)
- `vm_vcpu_count` - Number of vCPU cores (single socket) and defaults to the source template's CPU configuration if not specified
- `vm_memory_mb` - Memory size in MB for the VM (defaults to the source template's memory size if not specified)

#### Network Configuration
- `network_name` - Name of the OLVM network to attach to the VM
- `vnic_profile` - vNIC profile to use for the network interface (defaults to `network_name` if not specified)
- `dns_servers` - List of DNS server IP addresses to configure on the VM via cloud-init
- `address` - Static IP address for the VM
- `netmask` - Network mask (defaults to "255.255.255.0")
- `gateway` - Gateway address

#### Template Creation
- `destination_template_name` - Name for the template created from the VM. Defaults to "packer-<source_template_name>-<epoch_timestamp>".
- `destination_template_description` - Description for the template. Defaults to "Template created by Packer from VM <vm_name>".

#### OVA Export Configuration
- `export_host` - Host to which the template will be exported as an OVA file. If set, export will be performed.
- `export_directory` - Directory on `export_host` for the OVA file. Defaults to `/tmp` if not set, but only with `export_host`.
- `export_file_name` - Name of the OVA file. Defaults to `<destination_template_name>.ova` if not set, but only with `export_host`.

> **Note:** If either `export_directory` or `export_file_name` are set, `export_host` must also be set. If `export_host` is not set, no export will be performed.

#### Cleanup Configuration
- `cleanup_vm` - Enable VM cleanup after template creation (defaults to true). When disabled, the VM will not be deleted.
- `cleanup_interfaces` - Remove all network interfaces from the VM before template creation (defaults to true).

### SSH Configuration

The builder supports standard Packer SSH configuration options:

- `ssh_username` - SSH username
- `ssh_password` - SSH password
- `ssh_private_key_file` - Path to SSH private key
- `ssh_timeout` - SSH connection timeout (defaults to 5m)
- `ssh_handshake_attempts` - Number of SSH handshake attempts (defaults to 10)
- `ssh_port` - SSH port (defaults to 22)
- `ssh_host` - SSH host (auto-detected from VM)
- `ssh_clear_authorized_keys` - Clear authorized keys after provisioning
- `ssh_pty` - Enable pseudo-terminal allocation
- `ssh_agent_auth` - Use SSH agent authentication
- `ssh_disable_agent_forwarding` - Disable SSH agent forwarding
- `ssh_bastion_host` - SSH bastion host
- `ssh_bastion_port` - SSH bastion port
- `ssh_bastion_username` - SSH bastion username
- `ssh_bastion_password` - SSH bastion password
- `ssh_bastion_private_key_file` - SSH bastion private key file
- `ssh_file_transfer_method` - SSH file transfer method
- `ssh_keep_alive_interval` - SSH keep alive interval
- `ssh_read_write_timeout` - SSH read/write timeout

## Example Usage

```hcl
source "olvm" "example" {
  olvm_url = "https://olvm.example.com/ovirt-engine/api"
  username  = "admin@internal"
  password  = "password"
  
  source_template_name    = "centos-8-template"
  source_template_version = 1
  
  vm_name = "packer-example-vm"
  cluster = "Default"
  
  # VM resource configuration (optional - defaults to template values)
  vm_vcpu_count = 2
  vm_memory_mb   = 4096
  
  address      = "192.168.1.100"
  netmask      = "255.255.255.0"
  gateway      = "192.168.1.1"
  network_name = "ovirtmgmt"
  vnic_profile = "ovirtmgmt"
  
  dns_servers = ["8.8.8.8", "8.8.4.4"]
  
  destination_template_name        = "my-custom-template"
  destination_template_description = "Custom template created by Packer"

  # OVA export example
  export_host      = "olvm-host01"
  export_directory = "/tmp"
  export_file_name = "custom-template.ova"
  
  cleanup_vm         = true
  cleanup_interfaces = true
  
  ssh_username = "root"
  ssh_timeout  = "30m"
}

build {
  sources = ["source.olvm.example"]
  
  provisioner "shell" {
    inline = [
      "echo 'Hello from OLVM!'",
      "yum update -y"
    ]
  }
}
```

## Build from source

1. Clone this GitHub repository locally.

2. Run this command from the root directory: 
```shell 
go build -ldflags="-X github.com/mikelaws/packer-plugin-olvm/version.VersionPrerelease=dev" -o packer-plugin-olvm
```

3. After you successfully compile, the `packer-plugin-olvm` plugin binary file is in the root directory. 

4. To install the compiled plugin, run the following command 
```shell
packer plugins install --path packer-plugin-olvm github.com/mikelaws/packer-plugin-olvm
```

### Build on *nix systems
Unix like systems with the make, sed, and grep commands installed can use the `make dev` to execute the build from source steps. 

## Running Acceptance Tests

Make sure to install the plugin locally using the steps in [Build from source](#build-from-source).

Once everything needed is set up, run:
```
PACKER_ACC=1 go test -count 1 -v ./... -timeout=120m
```

This will run the acceptance tests for all plugins in this set.

## Environment Variables

The following environment variables can be used instead of configuration options:

- `OLVM_URL` - OLVM API URL
- `OLVM_USERNAME` - OLVM username
- `OLVM_PASSWORD` - OLVM password

## Requirements

- [packer-plugin-sdk](https://github.com/hashicorp/packer-plugin-sdk) >= v0.5.2
- [Go](https://golang.org/doc/install) >= 1.20
- OLVM/oVirt environment with API access

## Packer Compatibility
This plugin is compatible with Packer >= v1.10.2

## License

This project is licensed under the MPL-2.0 License - see the [LICENSE](LICENSE) file for details.
