integration {
  name = "Oracle Linux Virtualization Manager (OLVM)"
  description = "The OLVM builder can be used to build and export custom OLVM templates from disk image or template sources."
  identifier = "packer/hashicorp/olvm"
  component {
    type = "builder"
    name = "OLVM"
    slug = "olvm"
  }
}
