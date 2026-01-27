package olvm

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

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

	// Remote URL source fields (disk images only, not templates)
	SourceDiskURL                string `mapstructure:"source_disk_url"`
	SourceDiskURLChecksum         string `mapstructure:"source_disk_url_checksum"`
	SourceDiskChecksumURL         string `mapstructure:"source_disk_checksum_url"`
	SourceDiskURLChecksumType     string `mapstructure:"source_disk_url_checksum_type"`
	SourceDiskUploadName          string `mapstructure:"source_disk_upload_name"`
	SourceDiskStorageDomain       string `mapstructure:"source_disk_storage_domain"`
	ForceUpload                   bool   `mapstructure:"force_upload"`
	ConvertRawSparseToPreallocated bool  `mapstructure:"convert_raw_sparse_to_preallocated"`

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
	hasURL := c.SourceDiskURL != ""

	if hasTemplate && hasDisk {
		errs = append(errs, errors.New("Cannot specify both template and disk source parameters. Use either source_template_name/id or source_disk_name/id"))
	}
	if hasURL && (hasTemplate || hasDisk) {
		errs = append(errs, errors.New("Cannot specify source_disk_url with template or disk source parameters. Use either source_disk_url or source_template_name/id or source_disk_name/id"))
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

	// Validate URL source parameters if URL source
	if c.sourceType == "url" {
		// Validate URL format
		if c.SourceDiskURL != "" {
			parsedURL, err := url.Parse(c.SourceDiskURL)
			if err != nil {
				errs = append(errs, fmt.Errorf("Invalid source_disk_url: %s", err))
			} else {
				// Only allow http and https schemes
				if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
					errs = append(errs, fmt.Errorf("source_disk_url must use http or https scheme, got: %s", parsedURL.Scheme))
				}
				
				// Check if URL points to an OVA file (templates not supported)
				urlLower := strings.ToLower(c.SourceDiskURL)
				if strings.HasSuffix(urlLower, ".ova") {
					errs = append(errs, errors.New(
						"OVA template files are not supported. Only disk image formats are supported: "+
						"raw, img, qcow2. Please use a disk image URL instead of a template/OVA file."))
				}
			}
		}

		// Validate checksum type if provided
		if c.SourceDiskURLChecksumType != "" {
			validChecksumTypes := []string{"md5", "sha1", "sha256", "sha512"}
			validType := false
			for _, t := range validChecksumTypes {
				if strings.ToLower(c.SourceDiskURLChecksumType) == t {
					validType = true
					c.SourceDiskURLChecksumType = strings.ToLower(c.SourceDiskURLChecksumType)
					break
				}
			}
			if !validType {
				errs = append(errs, fmt.Errorf("Invalid source_disk_url_checksum_type: %s. Must be one of: %v", c.SourceDiskURLChecksumType, validChecksumTypes))
			}
		}

		// Set default checksum type if checksum is provided but type is not
		if (c.SourceDiskURLChecksum != "" || c.SourceDiskChecksumURL != "") && c.SourceDiskURLChecksumType == "" {
			c.SourceDiskURLChecksumType = "sha256"
			log.Printf("Using default source_disk_url_checksum_type: %s", c.SourceDiskURLChecksumType)
		}

		// Set default source_disk_upload_name from URL filename if not provided
		if c.SourceDiskUploadName == "" {
			if c.SourceDiskURL != "" {
				// Extract filename from URL
				parsedURL, err := url.Parse(c.SourceDiskURL)
				if err == nil {
					// Get the last component of the path
					pathParts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
					if len(pathParts) > 0 {
						filename := pathParts[len(pathParts)-1]
						// Remove query parameters and fragments if present
						filename = strings.Split(filename, "?")[0]
						filename = strings.Split(filename, "#")[0]
						// Keep extension to preserve original filename
						c.SourceDiskUploadName = filename
						log.Printf("Using filename from URL as source_disk_upload_name: %s", c.SourceDiskUploadName)
					}
				}
				// If we still don't have a name, generate one
				if c.SourceDiskUploadName == "" {
					c.SourceDiskUploadName = fmt.Sprintf("packer-uploaded-%d", time.Now().Unix())
					log.Printf("Generated default source_disk_upload_name: %s", c.SourceDiskUploadName)
				}
			}
		}
	}

	// Check if no source parameters are provided at all
	if !hasTemplate && !hasDisk && !hasURL {
		errs = append(errs, errors.New("Either source_template_name/id, source_disk_name/id, or source_disk_url must be specified"))
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
	hasURL := c.SourceDiskURL != ""

	if hasURL {
		return "url"
	}

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
