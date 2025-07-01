package olvm

import (
	"errors"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/hashicorp/packer-plugin-sdk/template/interpolate"
)

// SourceConfig contains the various source properties for an OLVM image
type SourceConfig struct {
	Cluster string `mapstructure:"cluster"`

	SourceTemplateName    string `mapstructure:"source_template_name"`
	SourceTemplateVersion int    `mapstructure:"source_template_version"`
	SourceTemplateID      string `mapstructure:"source_template_id"`

	SourceDiskName string `mapstructure:"source_disk_name"`
	SourceDiskID   string `mapstructure:"source_disk_id"`

	// Derived source type (not configurable)
	sourceType string
}

// Prepare performs basic validation on the SourceConfig
func (c *SourceConfig) Prepare(ctx *interpolate.Context) []error {
	var errs []error

	if c.Cluster == "" {
		c.Cluster = "Default"
	}

	// Determine source type based on provided parameters
	c.sourceType = c.deriveSourceType()

	// Check for conflicting parameters
	hasTemplate := (c.SourceTemplateName != "") || (c.SourceTemplateID != "")
	hasDisk := (c.SourceDiskName != "") || (c.SourceDiskID != "")
	if hasTemplate && hasDisk {
		errs = append(errs, errors.New("Cannot specify both template and disk source parameters. Use either source_template_name/id or source_disk_name/id"))
	}

	// Validate template parameters if template source
	if c.sourceType == "template" {
		if (c.SourceTemplateName != "") && (c.SourceTemplateVersion < 1) {
			c.SourceTemplateVersion = 1
			log.Printf("Using default source_template_version: %d", c.SourceTemplateVersion)
		}
		if c.SourceTemplateID != "" {
			if _, err := uuid.Parse(c.SourceTemplateID); err != nil {
				errs = append(errs, fmt.Errorf("Invalid source_template_id: %s", c.SourceTemplateID))
			}
		}
		if (c.SourceTemplateName != "") && (c.SourceTemplateID != "") {
			errs = append(errs, errors.New("Conflict: Set either source_template_name or source_template_id"))
		}
	}

	// Validate disk parameters if disk source
	if c.sourceType == "disk" {
		if c.SourceDiskID != "" {
			if _, err := uuid.Parse(c.SourceDiskID); err != nil {
				errs = append(errs, fmt.Errorf("Invalid source_disk_id: %s", c.SourceDiskID))
			}
		}
		if (c.SourceDiskName != "") && (c.SourceDiskID != "") {
			errs = append(errs, errors.New("Conflict: Set either source_disk_name or source_disk_id"))
		}
	}

	// Check if no source parameters are provided at all
	if !hasTemplate && !hasDisk {
		errs = append(errs, errors.New("Either source_template_name/id or source_disk_name/id must be specified"))
	}

	if len(errs) > 0 {
		return errs
	}

	return nil
}

// deriveSourceType determines the source type based on provided parameters
func (c *SourceConfig) deriveSourceType() string {
	hasTemplate := (c.SourceTemplateName != "") || (c.SourceTemplateID != "")
	hasDisk := (c.SourceDiskName != "") || (c.SourceDiskID != "")

	if hasTemplate && hasDisk {
		// This will be caught by validation, but we need to return something
		return "template" // default fallback
	}

	if hasTemplate {
		return "template"
	}

	if hasDisk {
		return "disk"
	}

	// Default to template if no parameters provided
	return "template"
}

// GetSourceType returns the derived source type
func (c *SourceConfig) GetSourceType() string {
	return c.sourceType
}
