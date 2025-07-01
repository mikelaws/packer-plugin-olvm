# Packer Plugin for Oracle Linux Virtualization Manager (OLVM)

This plugin provides a builder for creating OLVM templates from either source templates or source disk images in an Oracle Linux Virtualization Manager environment.

## Installation

To install this plugin, copy and paste this code into your Packer configuration, then run [`packer init`](https://www.packer.io/docs/commands/init).

```hcl
packer {
  required_plugins {
    olvm = {
      source  = "github.com/mikelaws/olvm"
      version = ">= 1.0.0"
    }
  }
}
```

Alternatively, you can use `packer plugins install` to manage installation of this plugin.

```sh
$ packer plugins install github.com/mikelaws/olvm
```

## Components

The OLVM plugin provides a builder for creating customized templates from source templates or disk images in OLVM environments.

### Builders

- [olvm](/packer/integrations/mikelaws/olvm/latest/components/builder/olvm) - The OLVM builder creates customized templates from source templates or disk images in Oracle Linux Virtualization Manager environments.

## Features

- Creates a Packer build VM from an OLVM template or disk image
- Configures SSH access and networking, and supports static IPs
- Generate and optionally export template artifacts (in OVA format) for distribution
- Uses cloud-init for network and SSH authorized keys configuration
- Automatic VM cleanup and template creation
- Support for both template-based and disk-based source images
- Configurable network interface names for different operating systems
