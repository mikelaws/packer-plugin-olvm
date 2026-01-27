package olvm

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/packer-plugin-sdk/multistep"
	"github.com/hashicorp/packer-plugin-sdk/packer"
	ovirtsdk4 "github.com/ovirt/go-ovirt"
)

type stepUploadImageFromURL struct {
	Debug bool
}

func (s *stepUploadImageFromURL) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	config := state.Get("config").(*Config)
	ui := state.Get("ui").(packer.Ui)
	connWrapper := state.Get("connWrapper").(*ConnectionWrapper)

	if config.SourceConfig.GetSourceType() != "url" {
		// Not a URL source, skip this step
		return multistep.ActionContinue
	}

	ui.Say("Processing remote URL source...")

	// Step 1: Get expected checksum FIRST (before downloading image)
	// This allows us to check for duplicates without downloading the large image file
	var expectedChecksum string
	var checksumAlgorithm string
	
	if config.SourceConfig.SourceDiskURLChecksum != "" {
		expectedChecksum = strings.ToLower(config.SourceConfig.SourceDiskURLChecksum)
		checksumAlgorithm = config.SourceConfig.SourceDiskURLChecksumType
		if checksumAlgorithm == "" {
			checksumAlgorithm = "sha256" // default
		}
		ui.Message(fmt.Sprintf("Using provided checksum (%s): %s", checksumAlgorithm, expectedChecksum))
	} else if config.SourceConfig.SourceDiskChecksumURL != "" {
		ui.Say("Downloading checksum file...")
		var err error
		expectedChecksum, err = s.downloadChecksum(config.SourceConfig.SourceDiskChecksumURL, config)
		if err != nil {
			err = fmt.Errorf("Error downloading checksum: %s", err)
			ui.Error(err.Error())
			state.Put("error", err)
			return multistep.ActionHalt
		}
		expectedChecksum = strings.ToLower(expectedChecksum)
		checksumAlgorithm = config.SourceConfig.SourceDiskURLChecksumType
		if checksumAlgorithm == "" {
			checksumAlgorithm = "sha256" // default
		}
		ui.Message(fmt.Sprintf("Downloaded checksum (%s): %s", checksumAlgorithm, expectedChecksum))
	} else {
		ui.Message("No checksum provided - duplicate detection will be name-based only")
	}

	// Step 2: Check for duplicates BEFORE downloading (unless force_upload is set)
	if !config.ForceUpload {
		ui.Say("Checking for existing image/template in OLVM...")
		resourceID, isDuplicate, err := s.checkDuplicateImage(connWrapper, config, expectedChecksum, checksumAlgorithm, ui)
		if err != nil {
			// Check if this is a checksum mismatch error (should halt, not continue)
			if strings.Contains(err.Error(), "checksum mismatch") || strings.Contains(err.Error(), "cannot verify") {
				ui.Error(err.Error())
				state.Put("error", err)
				return multistep.ActionHalt
			}
			// Other errors are warnings - continue with upload
			ui.Message(fmt.Sprintf("Warning: Could not check for duplicates: %s. Proceeding with download/upload.", err))
		} else if isDuplicate {
			ui.Say(fmt.Sprintf("Found existing disk with matching name and checksum, reusing: %s", resourceID))
			state.Put("uploaded_resource_id", resourceID)
			state.Put("uploaded_resource_type", "disk")
			return multistep.ActionContinue
		}
	} else {
		ui.Message("Force upload enabled - skipping duplicate check")
	}

	// Step 3: Create temporary directory and download image
	tempDir, err := os.MkdirTemp("", "packer-olvm-*")
	if err != nil {
		err = fmt.Errorf("Error creating temporary directory: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}
	defer os.RemoveAll(tempDir)

	imagePath := filepath.Join(tempDir, filepath.Base(config.SourceConfig.SourceDiskURL))
	ui.Say(fmt.Sprintf("Downloading disk image from %s...", config.SourceConfig.SourceDiskURL))
	if err := s.downloadImage(config, imagePath, ui); err != nil {
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}
	
	// Check if downloaded file is an OVA template (not supported)
	urlLower := strings.ToLower(config.SourceConfig.SourceDiskURL)
	fileExt := strings.ToLower(filepath.Ext(imagePath))
	if strings.HasSuffix(urlLower, ".ova") || fileExt == ".ova" {
		// Also check file content/header if possible
		detectedFormat, err := detectImageFormat(imagePath)
		if err == nil && detectedFormat != "qcow2" && detectedFormat != "raw" {
			err = fmt.Errorf(
				"OVA template files are not supported. Only disk image formats are supported: raw, img, qcow2. "+
				"Please use a disk image URL instead of a template/OVA file. "+
				"Detected file type: %s", fileExt)
			state.Put("error", err)
			ui.Error(err.Error())
			return multistep.ActionHalt
		}
	}

	// Step 4: Validate checksum of downloaded image
	if expectedChecksum != "" {
		ui.Say("Validating downloaded image checksum...")
		ui.Message(fmt.Sprintf("Calculating %s checksum of downloaded image...", checksumAlgorithm))
		calculatedChecksum, err := s.calculateChecksum(imagePath, checksumAlgorithm)
		if err != nil {
			state.Put("error", err)
			ui.Error(err.Error())
			return multistep.ActionHalt
		}
		calculatedChecksum = strings.ToLower(calculatedChecksum)
		if calculatedChecksum != expectedChecksum {
			err := fmt.Errorf("Checksum mismatch! Expected: %s, Got: %s", expectedChecksum, calculatedChecksum)
			state.Put("error", err)
			ui.Error(err.Error())
			return multistep.ActionHalt
		}
		ui.Say("Checksum validation successful")
	}

	// Store checksum info for use during upload
	state.Put("source_checksum", expectedChecksum)
	state.Put("source_checksum_algorithm", checksumAlgorithm)

	// Get storage domain
	clusterID, err := s.getClusterID(connWrapper, config.Cluster)
	if err != nil {
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	storageDomainID, err := s.getStorageDomainID(connWrapper, config.SourceConfig.SourceDiskStorageDomain, clusterID, ui)
	if err != nil {
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	// Upload disk image with checksum in description for future duplicate detection
	// Format will be auto-detected from the file
	ui.Say("Uploading disk image to OLVM...")
	uploadedResourceID, err := s.uploadDiskImage(connWrapper, imagePath, config.SourceConfig.SourceDiskUploadName, storageDomainID, expectedChecksum, checksumAlgorithm, config, ui)

	if err != nil {
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	ui.Say(fmt.Sprintf("Successfully uploaded disk image: %s", uploadedResourceID))

	// Store resource info in state
	state.Put("uploaded_resource_id", uploadedResourceID)
	state.Put("uploaded_resource_type", "disk")

	return multistep.ActionContinue
}

func (s *stepUploadImageFromURL) Cleanup(state multistep.StateBag) {
	config := state.Get("config").(*Config)
	ui := state.Get("ui").(packer.Ui)
	connWrapper := state.Get("connWrapper").(*ConnectionWrapper)

	// Check if we uploaded a resource that should be cleaned up
	uploadedResourceID, ok := state.GetOk("uploaded_resource_id")
	if !ok {
		// No resource uploaded, nothing to clean up
		return
	}

	uploadedResourceType, ok := state.GetOk("uploaded_resource_type")
	if !ok {
		return
	}

	// Only clean up uploaded resources if:
	// 1. Build failed (error in state) AND
	// 2. Template was not successfully created (no template_id) AND
	// 3. VM was not successfully created (no vm_id) OR VM cleanup is enabled
	// This ensures we don't delete resources that are being used by a VM
	_, hasError := state.GetOk("error")
	_, hasTemplateID := state.GetOk("template_id")
	_, hasVMID := state.GetOk("vm_id")

	// If VM was created, let the VM cleanup handle everything
	// Only clean up uploaded resource if VM was never created
	if hasError && !hasTemplateID && !hasVMID {
		resourceID := uploadedResourceID.(string)
		resourceType := uploadedResourceType.(string)

		// Only clean up if cleanup_vm is enabled (user wants cleanup)
		if config.CleanupVM != nil && *config.CleanupVM {
			ui.Say(fmt.Sprintf("Cleaning up uploaded %s due to build failure (VM was not created): %s", resourceType, resourceID))

			if resourceType == "ova" {
				// Delete template
				err := connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
					_, err := conn.SystemService().
						TemplatesService().
						TemplateService(resourceID).
						Remove().
						Send()
					return err
				})
				if err != nil {
					ui.Error(fmt.Sprintf("Error cleaning up uploaded template: %s", err))
				} else {
					ui.Message(fmt.Sprintf("Cleaned up uploaded template: %s", resourceID))
				}
			} else {
				// Delete disk
				err := connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
					_, err := conn.SystemService().
						DisksService().
						DiskService(resourceID).
						Remove().
						Send()
					return err
				})
				if err != nil {
					ui.Error(fmt.Sprintf("Error cleaning up uploaded disk: %s", err))
				} else {
					ui.Message(fmt.Sprintf("Cleaned up uploaded disk: %s", resourceID))
				}
			}
		}
	}

	// Temporary files are cleaned up in Run() via defer
}

func (s *stepUploadImageFromURL) downloadImage(config *Config, destPath string, ui packer.Ui) error {
	client := &http.Client{
		Timeout: 30 * time.Minute, // Long timeout for large files
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: config.AccessConfig.TLSInsecure,
			},
		},
	}

	req, err := http.NewRequest("GET", config.SourceConfig.SourceDiskURL, nil)
	if err != nil {
		return fmt.Errorf("Error creating HTTP request: %s", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Error downloading image: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Error downloading image: HTTP %d %s", resp.StatusCode, resp.Status)
	}

	// Get file size for progress reporting
	fileSize := resp.ContentLength
	if fileSize == -1 {
		ui.Message("Downloading image (size unknown)...")
	} else {
		ui.Message(fmt.Sprintf("Downloading image (%.2f MB)...", float64(fileSize)/(1024*1024)))
	}

	outFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("Error creating destination file: %s", err)
	}
	defer outFile.Close()

	// Stream download with progress reporting
	buf := make([]byte, 32*1024) // 32KB buffer
	var downloaded int64
	lastProgress := time.Now()

	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			written, writeErr := outFile.Write(buf[:n])
			if writeErr != nil {
				return fmt.Errorf("Error writing to file: %s", writeErr)
			}
			downloaded += int64(written)

			// Report progress every 5 seconds
			if time.Since(lastProgress) >= 5*time.Second && fileSize > 0 {
				percent := float64(downloaded) / float64(fileSize) * 100
				ui.Message(fmt.Sprintf("Download progress: %.1f%% (%.2f MB / %.2f MB)", percent, float64(downloaded)/(1024*1024), float64(fileSize)/(1024*1024)))
				lastProgress = time.Now()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("Error reading response: %s", err)
		}
	}

	if fileSize > 0 {
		ui.Message(fmt.Sprintf("Download complete: %.2f MB", float64(downloaded)/(1024*1024)))
	} else {
		ui.Message(fmt.Sprintf("Download complete: %.2f MB", float64(downloaded)/(1024*1024)))
	}

	return nil
}

func (s *stepUploadImageFromURL) downloadChecksum(checksumURL string, config *Config) (string, error) {
	client := &http.Client{
		Timeout: 5 * time.Minute,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: config.AccessConfig.TLSInsecure,
			},
		},
	}

	resp, err := client.Get(checksumURL)
	if err != nil {
		return "", fmt.Errorf("Error downloading checksum file: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Error downloading checksum file: HTTP %d %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("Error reading checksum file: %s", err)
	}

	// Parse checksum file - handle multiple formats
	checksumContent := string(body)
	lines := strings.Split(checksumContent, "\n")
	imageFileName := filepath.Base(config.SourceConfig.SourceDiskURL)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Skip comment lines
		if strings.HasPrefix(line, "#") {
			continue
		}

		// Try to find line matching our image filename
		if strings.Contains(line, imageFileName) {
			// Check for BSD-style format: ALGO (filename) = checksum
			// e.g., SHA256 (Rocky-9-GenericCloud-Base-9.7.x86_64.qcow2) = 15d81d34...
			if strings.Contains(line, " = ") {
				parts := strings.SplitN(line, " = ", 2)
				if len(parts) == 2 {
					checksum := strings.TrimSpace(parts[1])
					if checksum != "" {
						return checksum, nil
					}
				}
			}

			// Format: checksum  filename or checksum *filename or checksum filename
			// e.g., 15d81d34...  Rocky-9-GenericCloud-Base-9.7.x86_64.qcow2
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				// First part should be the checksum (hex string)
				checksum := parts[0]
				// Validate it looks like a hex checksum (at least 32 chars for MD5)
				if isHexString(checksum) && len(checksum) >= 32 {
					return checksum, nil
				}
			}
		}
	}

	// If no matching line found, try first non-empty, non-comment line (plain checksum)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Check for BSD-style format without filename match (single entry file)
		if strings.Contains(line, " = ") {
			parts := strings.SplitN(line, " = ", 2)
			if len(parts) == 2 {
				checksum := strings.TrimSpace(parts[1])
				if checksum != "" && isHexString(checksum) {
					return checksum, nil
				}
			}
		}

		// Plain checksum or standard format
		parts := strings.Fields(line)
		if len(parts) >= 1 {
			checksum := parts[0]
			if isHexString(checksum) && len(checksum) >= 32 {
				return checksum, nil
			}
		}
	}

	return "", fmt.Errorf("Could not parse checksum from file")
}

// isHexString checks if a string contains only hexadecimal characters
func isHexString(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return len(s) > 0
}

func (s *stepUploadImageFromURL) calculateChecksum(filePath string, algorithm string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("Error opening file for checksum: %s", err)
	}
	defer file.Close()

	var hash []byte
	switch strings.ToLower(algorithm) {
	case "md5":
		h := md5.New()
		if _, err := io.Copy(h, file); err != nil {
			return "", fmt.Errorf("Error calculating MD5: %s", err)
		}
		hash = h.Sum(nil)
	case "sha1":
		h := sha1.New()
		if _, err := io.Copy(h, file); err != nil {
			return "", fmt.Errorf("Error calculating SHA1: %s", err)
		}
		hash = h.Sum(nil)
	case "sha256":
		h := sha256.New()
		if _, err := io.Copy(h, file); err != nil {
			return "", fmt.Errorf("Error calculating SHA256: %s", err)
		}
		hash = h.Sum(nil)
	case "sha512":
		h := sha512.New()
		if _, err := io.Copy(h, file); err != nil {
			return "", fmt.Errorf("Error calculating SHA512: %s", err)
		}
		hash = h.Sum(nil)
	default:
		return "", fmt.Errorf("Unsupported checksum algorithm: %s", algorithm)
	}

	return hex.EncodeToString(hash), nil
}

func (s *stepUploadImageFromURL) validateImageChecksum(config *Config, imagePath string, ui packer.Ui) error {
	var expectedChecksum string
	var err error

	if config.SourceConfig.SourceDiskChecksumURL != "" {
		ui.Message(fmt.Sprintf("Downloading checksum from %s...", config.SourceConfig.SourceDiskChecksumURL))
		expectedChecksum, err = s.downloadChecksum(config.SourceConfig.SourceDiskChecksumURL, config)
		if err != nil {
			return fmt.Errorf("Error downloading checksum: %s", err)
		}
	} else if config.SourceConfig.SourceDiskURLChecksum != "" {
		expectedChecksum = strings.TrimSpace(config.SourceConfig.SourceDiskURLChecksum)
	} else {
		return fmt.Errorf("No checksum provided for validation")
	}

	checksumType := config.SourceConfig.SourceDiskURLChecksumType
	if checksumType == "" {
		checksumType = "sha256" // default
	}
	ui.Message(fmt.Sprintf("Calculating %s checksum of downloaded image...", checksumType))
	actualChecksum, err := s.calculateChecksum(imagePath, checksumType)
	if err != nil {
		return fmt.Errorf("Error calculating checksum: %s", err)
	}

	expectedChecksum = strings.ToLower(strings.TrimSpace(expectedChecksum))
	actualChecksum = strings.ToLower(strings.TrimSpace(actualChecksum))

	if expectedChecksum != actualChecksum {
		return fmt.Errorf("Checksum mismatch! Expected: %s, Got: %s", expectedChecksum, actualChecksum)
	}

	return nil
}

func (s *stepUploadImageFromURL) checkDuplicateImage(connWrapper *ConnectionWrapper, config *Config, expectedChecksum string, checksumAlgorithm string, ui packer.Ui) (string, bool, error) {
	imageName := config.SourceConfig.SourceDiskUploadName

	// Only check for disks (OVA/templates not supported)
	{
		// Search for disk by name/alias
		var disksResp *ovirtsdk4.DisksServiceListResponse
		err := connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
			var err error
			disksResp, err = conn.SystemService().DisksService().List().
				Search(fmt.Sprintf("alias=%s", imageName)).
				Send()
			return err
		})
		if err != nil {
			return "", false, fmt.Errorf("Error searching disks: %s", err)
		}

		disks, _ := disksResp.Disks()
		
		// Also search by name if alias search didn't find anything
		if disks == nil || len(disks.Slice()) == 0 {
			err = connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
				var err error
				disksResp, err = conn.SystemService().DisksService().List().
					Search(fmt.Sprintf("name=%s", imageName)).
					Send()
				return err
			})
			if err != nil {
				return "", false, fmt.Errorf("Error searching disks by name: %s", err)
			}
			disks, _ = disksResp.Disks()
		}

		if disks != nil && len(disks.Slice()) > 0 {
			for _, disk := range disks.Slice() {
				diskID := disk.MustId()
				diskName, _ := disk.Alias()
				if diskName == "" {
					diskName, _ = disk.Name()
				}
				
				// Skip disks in bad states
				diskStatus, _ := disk.Status()
				if diskStatus != ovirtsdk4.DISKSTATUS_OK {
					ui.Message(fmt.Sprintf("Skipping disk '%s' (status: %s)", diskName, diskStatus))
					continue
				}
				
				ui.Message(fmt.Sprintf("Found disk with name '%s': %s", diskName, diskID))
				
				// If no checksum provided, we can't verify - warn but use it
				if expectedChecksum == "" {
					ui.Message("No checksum available for verification - using existing disk by name match")
					return diskID, true, nil
				}
				
				// Check checksum in description
				description, hasDesc := disk.Description()
				if hasDesc {
					storedAlgo, storedChecksum := parseChecksumFromDescription(description)
					if storedChecksum != "" {
						ui.Message(fmt.Sprintf("Found stored checksum in disk description: %s:%s", storedAlgo, storedChecksum))
						
						// Compare checksums (algorithm must match too)
						if storedAlgo == checksumAlgorithm && strings.ToLower(storedChecksum) == strings.ToLower(expectedChecksum) {
							ui.Message("Checksum matches - reusing existing disk")
							return diskID, true, nil
						} else {
							// Checksum mismatch - this is an error condition
							return "", false, fmt.Errorf("Disk '%s' exists but has checksum mismatch. Stored: %s:%s, Expected: %s:%s. Delete the existing disk or use force_upload=true",
								diskName, storedAlgo, storedChecksum, checksumAlgorithm, expectedChecksum)
						}
					}
				}
				
				// Disk exists but has no checksum in description - cannot verify
				return "", false, fmt.Errorf("Disk '%s' exists but cannot verify checksum (no checksum in description). Delete the existing disk, rename it, or use force_upload=true",
					diskName)
			}
		}
	}

	// No matching resource found
	ui.Message("No existing image/template found with matching name")
	return "", false, nil
}

// parseChecksumFromDescription extracts checksum from description in format [packer-checksum:<algo>:<checksum>]
func parseChecksumFromDescription(description string) (algorithm string, checksum string) {
	// Look for pattern [packer-checksum:<algo>:<checksum>]
	prefix := "[packer-checksum:"
	suffix := "]"
	
	startIdx := strings.Index(description, prefix)
	if startIdx == -1 {
		return "", ""
	}
	
	endIdx := strings.Index(description[startIdx:], suffix)
	if endIdx == -1 {
		return "", ""
	}
	
	// Extract the content between prefix and suffix
	content := description[startIdx+len(prefix) : startIdx+endIdx]
	
	// Split by : to get algorithm and checksum
	parts := strings.SplitN(content, ":", 2)
	if len(parts) != 2 {
		return "", ""
	}
	
	return parts[0], parts[1]
}

// formatChecksumForDescription creates the description string with checksum
func formatChecksumForDescription(algorithm string, checksum string) string {
	if algorithm == "" || checksum == "" {
		return ""
	}
	return fmt.Sprintf("[packer-checksum:%s:%s]", algorithm, strings.ToLower(checksum))
}

func (s *stepUploadImageFromURL) getClusterID(connWrapper *ConnectionWrapper, clusterName string) (string, error) {
	var cResp *ovirtsdk4.ClustersServiceListResponse
	err := connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
		var err error
		cResp, err = conn.SystemService().
			ClustersService().
			List().
			Send()
		return err
	})
	if err != nil {
		return "", fmt.Errorf("Error getting cluster list: %s", err)
	}

	if clusters, ok := cResp.Clusters(); ok {
		for _, cluster := range clusters.Slice() {
			if name, ok := cluster.Name(); ok {
				if name == clusterName {
					return cluster.MustId(), nil
				}
			}
		}
	}

	return "", fmt.Errorf("Could not find cluster '%s'", clusterName)
}

func (s *stepUploadImageFromURL) getStorageDomainID(connWrapper *ConnectionWrapper, storageDomainName string, clusterID string, ui packer.Ui) (string, error) {
	if storageDomainName != "" {
		// Search for specified storage domain
		var storageDomainsResp *ovirtsdk4.StorageDomainsServiceListResponse
		err := connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
			var err error
			storageDomainsResp, err = conn.SystemService().StorageDomainsService().List().
				Search(fmt.Sprintf("name=%s", storageDomainName)).
				Send()
			return err
		})
		if err != nil {
			return "", fmt.Errorf("Error searching storage domains: %s", err)
		}

		storageDomains, _ := storageDomainsResp.StorageDomains()
		if storageDomains != nil && len(storageDomains.Slice()) > 0 {
			storageDomain := storageDomains.Slice()[0]
			ui.Message(fmt.Sprintf("Using specified storage domain: %s", storageDomainName))
			return storageDomain.MustId(), nil
		}
		return "", fmt.Errorf("Could not find storage domain '%s'", storageDomainName)
	}

	// Get cluster default storage domain
	// Query storage domains and find one associated with the cluster
	ui.Message("Using cluster default storage domain...")
	var storageDomainsResp *ovirtsdk4.StorageDomainsServiceListResponse
	err := connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
		var err error
		storageDomainsResp, err = conn.SystemService().StorageDomainsService().List().Send()
		return err
	})
	if err != nil {
		return "", fmt.Errorf("Error listing storage domains: %s", err)
	}

	storageDomains, _ := storageDomainsResp.StorageDomains()
	if storageDomains != nil && len(storageDomains.Slice()) > 0 {
		// Find a storage domain associated with the cluster
		// Data storage domains are typically what we want
		for _, sd := range storageDomains.Slice() {
			// Check if it's a data domain (not ISO/Export)
			if sdType, ok := sd.Type(); ok && sdType == ovirtsdk4.STORAGEDOMAINTYPE_DATA {
				// Check if it's available and active
				if status, ok := sd.Status(); ok && status == ovirtsdk4.STORAGEDOMAINSTATUS_ACTIVE {
					storageDomainName, _ := sd.Name()
					ui.Message(fmt.Sprintf("Using cluster default storage domain: %s", storageDomainName))
					return sd.MustId(), nil
				}
			}
		}
		// If no active data domain found, use first available data domain
		for _, sd := range storageDomains.Slice() {
			if sdType, ok := sd.Type(); ok && sdType == ovirtsdk4.STORAGEDOMAINTYPE_DATA {
				storageDomainName, _ := sd.Name()
				ui.Message(fmt.Sprintf("Using storage domain: %s", storageDomainName))
				return sd.MustId(), nil
			}
		}
	}

	return "", fmt.Errorf("Could not determine storage domain for cluster")
}

// isBlockStorageDomain checks if the storage domain is block storage (iSCSI, FC, etc.)
// vs file storage (NFS, etc.). Block storage types: iscsi, fcp
// File storage types: nfs, posixfs, glusterfs, etc.
func (s *stepUploadImageFromURL) isBlockStorageDomain(connWrapper *ConnectionWrapper, storageDomainID string) (bool, error) {
	var storageDomain *ovirtsdk4.StorageDomain
	err := connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
		var err error
		sdResp, err := conn.SystemService().
			StorageDomainsService().
			StorageDomainService(storageDomainID).
			Get().
			Send()
		if err != nil {
			return err
		}
		storageDomain = sdResp.MustStorageDomain()
		return nil
	})
	if err != nil {
		return false, err
	}
	
	// Get storage type from storage domain's storage field
	storage, ok := storageDomain.Storage()
	if !ok {
		return false, fmt.Errorf("Could not determine storage domain storage type")
	}
	
	storageType, ok := storage.Type()
	if !ok {
		return false, fmt.Errorf("Could not determine storage type")
	}
	
	// Block storage types in oVirt/OLVM
	// StorageType.ISCSI and StorageType.FCP are block storage
	// All others (NFS, POSIXFS, GLUSTERFS, etc.) are file storage
	// Convert to string for comparison as constants may vary
	storageTypeStr := strings.ToLower(string(storageType))
	
	// Check if it's block storage (iSCSI or FCP)
	isBlock := storageTypeStr == "iscsi" || storageTypeStr == "fcp"
	
	return isBlock, nil
}

// isSparseFile detects if a RAW file is sparse
// For our use case, we assume RAW files from URLs may be sparse
// The conversion will work correctly whether the file is sparse or preallocated
func (s *stepUploadImageFromURL) isSparseFile(filePath string) (bool, error) {
	// For RAW files downloaded from URLs, they're typically sparse unless explicitly preallocated
	// We'll be conservative and assume they might be sparse
	// The qemu-img convert operation will work correctly for both sparse and preallocated files
	return true, nil
}

// convertRawSparseToPreallocated converts a sparse RAW image to a preallocated RAW image using qemu-img
// This is needed for RAW images on block storage, as sparse RAW is not supported
func (s *stepUploadImageFromURL) convertRawSparseToPreallocated(sparsePath string, preallocatedPath string, ui packer.Ui) error {
	ui.Message(fmt.Sprintf("Converting sparse RAW image to preallocated RAW image..."))
	ui.Message(fmt.Sprintf("Source: %s", sparsePath))
	ui.Message(fmt.Sprintf("Target: %s", preallocatedPath))
	
	// Check if qemu-img is available
	qemuImgPath, err := exec.LookPath("qemu-img")
	if err != nil {
		return fmt.Errorf("qemu-img not found in PATH. QEMU tools must be installed for RAW sparse to preallocated conversion")
	}
	
	// Run qemu-img convert: qemu-img convert -f raw -O raw -o preallocation=full input.raw output.raw
	// The -o preallocation=full option creates a fully preallocated RAW file
	cmd := exec.Command(qemuImgPath, "convert", "-f", "raw", "-O", "raw", "-o", "preallocation=full", sparsePath, preallocatedPath)
	
	// Capture output for progress/errors
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	
	ui.Message("Running qemu-img convert to preallocated RAW (this may take a while for large images)...")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("qemu-img convert failed: %s\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	
	// Check if output file was created
	if _, err := os.Stat(preallocatedPath); os.IsNotExist(err) {
		return fmt.Errorf("Preallocated RAW file was not created: %s", preallocatedPath)
	}
	
	// Get file size for reporting
	preallocatedInfo, err := os.Stat(preallocatedPath)
	if err != nil {
		return fmt.Errorf("Error getting preallocated RAW file info: %s", err)
	}
	
	sparseInfo, err := os.Stat(sparsePath)
	if err != nil {
		return fmt.Errorf("Error getting sparse RAW file info: %s", err)
	}
	
	ui.Message(fmt.Sprintf("Conversion complete. Preallocated RAW size: %.2f MB (sparse RAW was: %.2f MB)", 
		float64(preallocatedInfo.Size())/(1024*1024), 
		float64(sparseInfo.Size())/(1024*1024)))
	
	return nil
}

func (s *stepUploadImageFromURL) uploadDiskImage(connWrapper *ConnectionWrapper, filePath string, imageName string, storageDomainID string, checksum string, checksumAlgorithm string, config *Config, ui packer.Ui) (string, error) {
	// Get file size
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return "", fmt.Errorf("Error getting file info: %s", err)
	}
	fileSize := fileInfo.Size()

	// Auto-detect image format from file header
	format, err := detectImageFormat(filePath)
	if err != nil {
		return "", fmt.Errorf("Error detecting image format: %s. Only raw, img, and qcow2 disk image formats are supported.", err)
	}
	
	// Check if format is OVA (not supported)
	if format == "ova" || strings.HasSuffix(strings.ToLower(filePath), ".ova") {
		return "", fmt.Errorf(
			"OVA template files are not supported. Only disk image formats are supported: raw, img, qcow2. "+
			"Please use a disk image URL instead of a template/OVA file.")
	}
	
	ui.Message(fmt.Sprintf("Detected disk image format: %s", format))
	ui.Message(fmt.Sprintf("Uploading disk '%s' (format: %s, size: %.2f MB) to storage domain...", imageName, format, float64(fileSize)/(1024*1024)))

	// Get storage domain type to determine if we need to convert RAW sparse to RAW preallocated
	// Workaround for bug: RAW Sparse disks are incompatible with block storage domains
	// See: https://bugzilla.redhat.com/show_bug.cgi?id=1957830
	isBlockStorage, err := s.isBlockStorageDomain(connWrapper, storageDomainID)
	if err != nil {
		ui.Message(fmt.Sprintf("Warning: Could not determine storage domain type: %s. Proceeding with default settings.", err))
		isBlockStorage = false
	} else if isBlockStorage {
		ui.Message("Detected block storage domain (iSCSI/FCP)")
	}
	
	// For RAW format on block storage, handle sparse RAW files
	// RAW sparse images are not supported on block storage (Bug 1957830)
	// See: https://bugzilla.redhat.com/show_bug.cgi?id=1957830
	var convertedFilePath string
	
	if format == "raw" && isBlockStorage {
		// RAW images on block storage may be sparse, which is not supported (Bug 1957830)
		// Check user configuration for conversion preference
		if !config.SourceConfig.ConvertRawSparseToPreallocated {
			return "", fmt.Errorf(
				"RAW images on block storage domains (iSCSI/FCP) may be sparse, which is not supported by OLVM. "+
				"This is a known limitation (see Bug 1957830: https://bugzilla.redhat.com/show_bug.cgi?id=1957830). "+
				"\n\nTo resolve this, set 'convert_raw_sparse_to_preallocated = true' in your configuration "+
				"to automatically convert sparse RAW images to preallocated RAW images before upload. "+
				"\n\nAlternatively:\n"+
				"  - Use a file storage domain (NFS) which supports RAW sparse images\n"+
				"  - Convert the image to QCOW2 format which works on both storage types\n"+
				"  - Pre-allocate the RAW image before uploading")
		}
		
		// User has enabled conversion - convert sparse RAW to preallocated RAW
		// Note: This conversion works for both sparse and preallocated RAW files
		ui.Message("RAW format detected on block storage - converting to preallocated RAW (convert_raw_sparse_to_preallocated is enabled)...")
		
		// Convert RAW sparse to RAW preallocated
		convertedFilePath = filePath + ".preallocated"
		if err := s.convertRawSparseToPreallocated(filePath, convertedFilePath, ui); err != nil {
			return "", fmt.Errorf("Error converting RAW sparse to preallocated: %s", err)
		}
		
		// Update file path for upload
		filePath = convertedFilePath
		
		// Update file size after conversion (preallocated will be same or larger)
		fileInfo, err = os.Stat(filePath)
		if err != nil {
			return "", fmt.Errorf("Error getting converted file info: %s", err)
		}
		fileSize = fileInfo.Size()
		
		// Clean up converted file after upload
		defer func() {
			if convertedFilePath != "" {
				if err := os.Remove(convertedFilePath); err != nil {
					ui.Message(fmt.Sprintf("Warning: Could not remove temporary preallocated RAW file: %s", err))
				}
			}
		}()
	}
	
	// Determine disk format
	var diskFormat ovirtsdk4.DiskFormat
	switch format {
	case "qcow2":
		diskFormat = ovirtsdk4.DISKFORMAT_COW
	case "raw":
		diskFormat = ovirtsdk4.DISKFORMAT_RAW
	default:
		return "", fmt.Errorf("Unsupported disk format: %s", format)
	}

	// Create disk first (required by ImageTransfer API)
	ui.Message(fmt.Sprintf("Creating disk '%s' (format: %s)...", imageName, format))
	
	// Determine provisioned size and sparse setting based on format and storage type
	// Different formats have different size requirements:
	// - raw: File size = disk size (exact match required)
	// - qcow2: File size is compressed, need virtual size from header
	// - ova: Archive format, contains disk images inside (handled separately)
	var provisionedSize int64
	useSparse := true
	
	if format == "qcow2" {
		// Parse qcow2 header to get actual virtual size
		ui.Message("Parsing qcow2 header to determine virtual disk size...")
		virtualSize, err := parseQcow2VirtualSize(filePath)
		if err != nil {
			// Fallback to 2x file size if parsing fails
			ui.Message(fmt.Sprintf("Warning: Could not parse qcow2 header (%s), using estimated size", err))
			provisionedSize = int64(fileSize) * 2
			ui.Message(fmt.Sprintf("Using estimated virtual size: %.2f MB (file size: %.2f MB)", float64(provisionedSize)/(1024*1024), float64(fileSize)/(1024*1024)))
		} else {
			provisionedSize = virtualSize
			ui.Message(fmt.Sprintf("Parsed qcow2 virtual size: %.2f MB (file size: %.2f MB, compression: %.1f%%)", 
				float64(provisionedSize)/(1024*1024), 
				float64(fileSize)/(1024*1024),
				float64(fileSize)/float64(provisionedSize)*100))
		}
	} else if format == "raw" {
		// For raw images, file size equals disk size exactly
		provisionedSize = int64(fileSize)
		ui.Message(fmt.Sprintf("Raw image size: %.2f MB", float64(provisionedSize)/(1024*1024)))
		
		// For RAW on block storage, use preallocated (not sparse)
		// RAW sparse is incompatible with block storage (Bug 1957830)
		if isBlockStorage {
			useSparse = false
			ui.Message("Using preallocated RAW disk for block storage")
		} else {
			useSparse = true
			ui.Message("Using sparse RAW disk for file storage")
		}
	} else {
		// For other formats (like ova), we'll handle separately
		// For now, use file size as fallback
		provisionedSize = int64(fileSize)
		ui.Message(fmt.Sprintf("Using file size for format %s: %.2f MB", format, float64(provisionedSize)/(1024*1024)))
	}
	
	// Build description with checksum for future duplicate detection
	diskDescription := formatChecksumForDescription(checksumAlgorithm, checksum)
	if diskDescription != "" {
		ui.Message(fmt.Sprintf("Storing checksum in disk description: %s", diskDescription))
	}
	
	diskBuilder := ovirtsdk4.NewDiskBuilder().
		Name(imageName).
		Format(diskFormat).
		ProvisionedSize(provisionedSize).
		Sparse(useSparse).
		StorageDomainsOfAny(
			ovirtsdk4.NewStorageDomainBuilder().
				Id(storageDomainID).
				MustBuild(),
		)
	
	// Note: For preallocated disks (sparse=false), we don't set InitialSize
	// The Image Transfer API will handle the allocation during upload
	
	// Add description with checksum if available
	if diskDescription != "" {
		diskBuilder.Description(diskDescription)
	}
	
	disk, err := diskBuilder.Build()
	if err != nil {
		return "", fmt.Errorf("Error creating disk object: %s", err)
	}

	var diskAddResp *ovirtsdk4.DisksServiceAddResponse
	err = connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
		var err error
		diskAddResp, err = conn.SystemService().
			DisksService().
			Add().
			Disk(disk).
			Send()
		return err
	})
	if err != nil {
		return "", fmt.Errorf("Error creating disk: %s", err)
	}

	createdDisk := diskAddResp.MustDisk()
	diskID := createdDisk.MustId()
	ui.Message(fmt.Sprintf("Created disk: %s", diskID))

	// Wait for disk to be ready (unlocked) before starting transfer
	// The disk is locked immediately after creation, we need to wait for it to be available
	ui.Message("Waiting for disk to become available for transfer...")
	diskStateChange := StateChangeConf{
		Pending:   []string{"locked", "image_locked"},
		Target:    []string{string(ovirtsdk4.DISKSTATUS_OK)},
		Refresh:   DiskStateRefreshFuncWithWrapper(connWrapper, diskID),
		StepState: nil,
	}
	if _, err := WaitForState(&diskStateChange); err != nil {
		return "", fmt.Errorf("Error waiting for disk to be ready: %s", err)
	}
	ui.Message("Disk is ready for transfer")

	// Initialize image transfer
	ui.Message("Initializing image transfer...")
	var imageTransferResp *ovirtsdk4.ImageTransfersServiceAddResponse
	err = connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
		var err error
		imageTransfer, err := ovirtsdk4.NewImageTransferBuilder().
			Disk(
				ovirtsdk4.NewDiskBuilder().
					Id(diskID).
					MustBuild(),
			).
			Direction(ovirtsdk4.IMAGETRANSFERDIRECTION_UPLOAD).
			Build()
		if err != nil {
			return fmt.Errorf("Error creating image transfer object: %s", err)
		}

		imageTransferResp, err = conn.SystemService().
			ImageTransfersService().
			Add().
			ImageTransfer(imageTransfer).
			Send()
		return err
	})
	if err != nil {
		return "", fmt.Errorf("Error initializing image transfer: %s", err)
	}

	imageTransfer := imageTransferResp.MustImageTransfer()
	transferURL, _ := imageTransfer.TransferUrl()
	transferID := imageTransfer.MustId()
	ui.Message(fmt.Sprintf("Image transfer initialized for disk: %s, uploading to: %s", diskID, transferURL))

	// Upload image data
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("Error opening image file: %s", err)
	}
	defer file.Close()

	// Create HTTP client for upload
	uploadClient := &http.Client{
		Timeout: 2 * time.Hour, // Long timeout for large uploads
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // Image transfer typically uses self-signed certs
			},
		},
	}

	// Prepare upload request
	req, err := http.NewRequest("PUT", transferURL, file)
	if err != nil {
		return "", fmt.Errorf("Error creating upload request: %s", err)
	}
	req.ContentLength = fileSize
	req.Header.Set("Content-Type", "application/octet-stream")

	// Upload with progress reporting
	ui.Message("Uploading image data...")
	lastProgress := time.Now()
	progressReader := &progressReader{
		reader:       file,
		total:        fileSize,
		ui:           ui,
		lastProgress: &lastProgress,
	}

	req.Body = io.NopCloser(progressReader)

	resp, err := uploadClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("Error uploading image: %s", err)
	}
	defer resp.Body.Close()

	// Read response body to ensure upload is complete
	_, err = io.Copy(io.Discard, resp.Body)
	if err != nil {
		return "", fmt.Errorf("Error reading upload response: %s", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return "", fmt.Errorf("Error uploading image: HTTP %d %s", resp.StatusCode, resp.Status)
	}

	ui.Message("Image data uploaded, checking transfer status...")
	// transferID was already set above

	// Check transfer status and handle paused states
	// We need to finalize while in "transferring" state, not wait for "finished"
	maxRetries := 5
	for i := 0; i < maxRetries; i++ {
		var transferResp *ovirtsdk4.ImageTransferServiceGetResponse
		err := connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
			var err error
			transferResp, err = conn.SystemService().
				ImageTransfersService().
				ImageTransferService(transferID).
				Get().
				Send()
			return err
		})
		if err != nil {
			if _, ok := err.(*ovirtsdk4.NotFoundError); ok {
				// Transfer already completed and was deleted
				ui.Message("Transfer already completed")
				break
			}
			return "", fmt.Errorf("Error checking transfer status: %s", err)
		}

		transfer := transferResp.MustImageTransfer()
		phase, _ := transfer.Phase()
		phaseStr := string(phase)
		ui.Message(fmt.Sprintf("Transfer phase: %s", phaseStr))

		// Handle paused_system state - need to resume
		if phaseStr == "paused_system" {
			ui.Message("Transfer paused by system, attempting to resume...")
			err = connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
				_, err := conn.SystemService().
					ImageTransfersService().
					ImageTransferService(transferID).
					Resume().
					Send()
				return err
			})
			if err != nil {
				return "", fmt.Errorf("Error resuming paused transfer: %s", err)
			}
			ui.Message("Transfer resumed, waiting for transferring state...")
			time.Sleep(2 * time.Second)
			continue
		}

		// Handle paused_user state - need to resume
		if phaseStr == "paused_user" {
			ui.Message("Transfer paused by user, attempting to resume...")
			err = connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
				_, err := conn.SystemService().
					ImageTransfersService().
					ImageTransferService(transferID).
					Resume().
					Send()
				return err
			})
			if err != nil {
				return "", fmt.Errorf("Error resuming paused transfer: %s", err)
			}
			ui.Message("Transfer resumed, waiting for transferring state...")
			time.Sleep(2 * time.Second)
			continue
		}

		// If transfer is already in a final state, check if it succeeded
		if phaseStr == "finished_success" {
			ui.Message("Transfer completed successfully")
			break
		}
		if phaseStr == "finished_failure" || phaseStr == "finalizing_failure" {
			return "", fmt.Errorf("Transfer failed with phase: %s", phaseStr)
		}

		// If in transferring state, we can finalize
		if phaseStr == "transferring" {
			ui.Message("Transfer in transferring state, finalizing...")
			break
		}

		// If in resuming state, wait a bit and check again
		if phaseStr == "resuming" {
			ui.Message("Transfer resuming, waiting...")
			time.Sleep(2 * time.Second)
			continue
		}

		// For other states, wait a bit and retry
		if i < maxRetries-1 {
			time.Sleep(2 * time.Second)
		}
	}

	// Finalize transfer (should be in "transferring" state)
	ui.Message("Finalizing transfer...")
	err = connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
		// Finalize the transfer
		_, err := conn.SystemService().
			ImageTransfersService().
			ImageTransferService(transferID).
			Finalize().
			Send()
		return err
	})
	if err != nil {
		// Try to get more details about the failure
		var transferResp *ovirtsdk4.ImageTransferServiceGetResponse
		checkErr := connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
			var err error
			transferResp, err = conn.SystemService().
				ImageTransfersService().
				ImageTransferService(transferID).
				Get().
				Send()
			return err
		})
		if checkErr == nil && transferResp != nil {
			transfer := transferResp.MustImageTransfer()
			if phase, ok := transfer.Phase(); ok {
				return "", fmt.Errorf("Error finalizing image transfer (phase: %s): %s", phase, err)
			}
		}
		return "", fmt.Errorf("Error finalizing image transfer: %s", err)
	}
	ui.Message("Transfer finalized, waiting for completion...")

	// Wait for transfer to reach finished_success
	// Monitor for both success and failure states
	transferStateChange := StateChangeConf{
		Pending:   []string{"finalizing_success", "resuming"},
		Target:    []string{"finished_success", "finished_failure", "finalizing_failure"},
		Refresh:   ImageTransferStateRefreshFuncWithWrapper(connWrapper, transferID),
		StepState: nil,
	}
	_, err = WaitForState(&transferStateChange)
	
	// Check the final state regardless of WaitForState result
	var finalPhase string
	var transferResp *ovirtsdk4.ImageTransferServiceGetResponse
	checkErr := connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
		var err error
		transferResp, err = conn.SystemService().
			ImageTransfersService().
			ImageTransferService(transferID).
			Get().
			Send()
		return err
	})
	if checkErr == nil && transferResp != nil {
		transfer := transferResp.MustImageTransfer()
		if phase, ok := transfer.Phase(); ok {
			finalPhase = string(phase)
		}
	} else if _, ok := checkErr.(*ovirtsdk4.NotFoundError); ok {
		// Transfer deleted is normal after finished_success
		finalPhase = "deleted_after_success"
	}
	
	// Log transfer phase, but don't fail yet - wait for disk to finish processing
	if finalPhase == "finished_success" || finalPhase == "deleted_after_success" {
		ui.Message("Transfer completed successfully")
	} else if finalPhase == "finished_failure" || finalPhase == "finalizing_failure" {
		ui.Message(fmt.Sprintf("Transfer phase indicates potential failure: %s, but waiting for disk processing to complete...", finalPhase))
	} else if finalPhase != "" {
		ui.Message(fmt.Sprintf("Transfer phase: %s", finalPhase))
	}
	
	if err != nil && finalPhase != "deleted_after_success" && finalPhase != "finished_success" {
		ui.Message(fmt.Sprintf("Warning: Error waiting for transfer completion: %s (final phase: %s). Waiting for disk to finish processing...", err, finalPhase))
	}

	// Wait for disk to finish processing (locked -> ok or illegal)
	// This is critical - even if transfer phase shows failure, the disk might still be processing
	ui.Message("Waiting for disk to finish processing (may be in 'locked' state)...")
	diskStateChange = StateChangeConf{
		Pending:   []string{"locked", "image_locked"},
		Target:    []string{string(ovirtsdk4.DISKSTATUS_OK), "illegal"},
		Refresh:   DiskStateRefreshFuncWithWrapper(connWrapper, diskID),
		StepState: nil,
	}
	_, err = WaitForState(&diskStateChange)
	
	// Now check the final disk state to determine success/failure
	var diskResp *ovirtsdk4.DiskServiceGetResponse
	diskCheckErr := connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
		var err error
		diskResp, err = conn.SystemService().
			DisksService().
			DiskService(diskID).
			Get().
			Send()
		return err
	})
	
	if diskCheckErr == nil && diskResp != nil {
		disk := diskResp.MustDisk()
		diskStatus := string(disk.MustStatus())
		
		if diskStatus == "illegal" {
			// Disk is in illegal state - this is a real failure
			// Get detailed error information from disk comment and other fields
			errorMsg := fmt.Sprintf("Image transfer failed. Disk is in 'illegal' state after processing.")
			
			// OLVM often stores detailed error messages in the disk comment
			if comment, ok := disk.Comment(); ok && comment != "" {
				errorMsg += fmt.Sprintf("\nDisk comment (OLVM error details): %s", comment)
			} else {
				errorMsg += " (No error details in disk comment)"
			}
			
			// Include transfer phase for context
			errorMsg += fmt.Sprintf("\nTransfer phase was: %s", finalPhase)
			
			// Include disk and storage information
			if diskName, ok := disk.Name(); ok {
				errorMsg += fmt.Sprintf("\nDisk name: %s", diskName)
			}
			if diskID, ok := disk.Id(); ok {
				errorMsg += fmt.Sprintf("\nDisk ID: %s", diskID)
			}
			
			// Add format-specific error guidance
			if format == "qcow2" {
				errorMsg += fmt.Sprintf(". For qcow2 images, the disk provisioned_size must be large enough to accommodate the virtual disk size (not just the compressed file size). Current provisioned size: %.2f MB, file size: %.2f MB", float64(provisionedSize)/(1024*1024), float64(fileSize)/(1024*1024))
			} else if format == "raw" {
				errorMsg += fmt.Sprintf(". For raw images, the disk provisioned_size must match the file size exactly. Current provisioned size: %d bytes (%.2f MB), file size: %d bytes (%.2f MB)", provisionedSize, float64(provisionedSize)/(1024*1024), fileSize, float64(fileSize)/(1024*1024))
				// Check if this is block storage for additional guidance
				if blockStorage, _ := s.isBlockStorageDomain(connWrapper, storageDomainID); blockStorage {
					errorMsg += " For preallocated RAW disks on block storage, size must be aligned to block boundaries (typically 512 bytes or 4KB)."
				}
			} else {
				errorMsg += fmt.Sprintf(". Current provisioned size: %.2f MB, file size: %.2f MB", float64(provisionedSize)/(1024*1024), float64(fileSize)/(1024*1024))
			}
			return "", fmt.Errorf("%s", errorMsg)
		} else if diskStatus == string(ovirtsdk4.DISKSTATUS_OK) {
			// Disk is OK - success, even if transfer phase showed failure initially
			ui.Message("Disk is in 'ok' state - upload successful")
		} else {
			// Unexpected state
			return "", fmt.Errorf("Disk is in unexpected state '%s' after processing. Transfer phase was: %s", diskStatus, finalPhase)
		}
	} else if err != nil {
		// WaitForState failed - check if we can get disk status
		return "", fmt.Errorf("Error waiting for disk to finish processing: %s", err)
	}

	ui.Message("Disk upload completed successfully")
	return diskID, nil
}

func (s *stepUploadImageFromURL) uploadTemplate(connWrapper *ConnectionWrapper, filePath string, templateName string, storageDomainID string, checksum string, checksumAlgorithm string, config *Config, ui packer.Ui) (string, error) {
	// For OVA templates, we'll use the TemplatesService import functionality
	// This is similar to how templates are created from VMs
	// Note: OVA import may require additional steps depending on OLVM version

	ui.Message(fmt.Sprintf("Importing OVA template '%s'...", templateName))

	// Get file size for progress
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return "", fmt.Errorf("Error getting file info: %s", err)
	}
	fileSize := fileInfo.Size()
	ui.Message(fmt.Sprintf("Template size: %.2f MB", float64(fileSize)/(1024*1024)))

	// For OVA import, we typically need to:
	// 1. Create a temporary VM from the OVA
	// 2. Convert that VM to a template
	// However, OLVM may have direct OVA import - this needs to be tested

	// For now, we'll use a simplified approach: treat OVA as a disk image
	// and create a template from it
	// This is a placeholder - actual implementation may vary based on OLVM API capabilities

	ui.Message("Note: OVA template import may require additional steps. Using disk-based approach...")

	// Upload as disk first, then we can create a template from it
	// This is a workaround until we can verify direct OVA import API
	_, err = s.uploadDiskImage(connWrapper, filePath, templateName, storageDomainID, checksum, checksumAlgorithm, config, ui)
	if err != nil {
		return "", fmt.Errorf("Error uploading OVA as disk: %s", err)
	}

	// Build description with checksum for future duplicate detection
	templateDescription := formatChecksumForDescription(checksumAlgorithm, checksum)

	// TODO: Create template from the uploaded disk
	// For now, create an empty template as placeholder
	// This needs to be enhanced to properly import OVA templates
	ui.Message("Creating template from uploaded disk...")
	templateBuilder := ovirtsdk4.NewTemplateBuilder().
		Name(templateName)
	
	// Add description with checksum if available
	if templateDescription != "" {
		templateBuilder.Description(templateDescription)
		ui.Message(fmt.Sprintf("Storing checksum in template description: %s", templateDescription))
	}
	
	template, err := templateBuilder.Build()
	if err != nil {
		return "", fmt.Errorf("Error creating template object: %s", err)
	}

	var templateAddResp *ovirtsdk4.TemplatesServiceAddResponse
	err = connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
		var err error
		templateAddResp, err = conn.SystemService().
			TemplatesService().
			Add().
			Template(template).
			Send()
		return err
	})
	if err != nil {
		return "", fmt.Errorf("Error creating template: %s", err)
	}

	createdTemplate := templateAddResp.MustTemplate()
	templateID := createdTemplate.MustId()

	// Attach disk to template
	// This is a simplified approach - actual OVA import may be different
	ui.Message("Note: OVA import completed. Template ID: " + templateID)
	ui.Message("Warning: Direct OVA import may require additional configuration. Please verify template is correct.")

	return templateID, nil
}

// progressReader wraps an io.Reader to report progress
type progressReader struct {
	reader       io.Reader
	total        int64
	read         int64
	ui           packer.Ui
	lastProgress *time.Time
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	pr.read += int64(n)

	// Report progress every 5 seconds
	if time.Since(*pr.lastProgress) >= 5*time.Second {
		percent := float64(pr.read) / float64(pr.total) * 100
		pr.ui.Message(fmt.Sprintf("Upload progress: %.1f%% (%.2f MB / %.2f MB)", percent, float64(pr.read)/(1024*1024), float64(pr.total)/(1024*1024)))
		*pr.lastProgress = time.Now()
	}

	return n, err
}

// DiskStateRefreshFuncWithWrapper returns a StateRefreshFunc for disk status with reconnection support
func DiskStateRefreshFuncWithWrapper(connWrapper *ConnectionWrapper, diskID string) StateRefreshFunc {
	return func() (interface{}, string, error) {
		var resp *ovirtsdk4.DiskServiceGetResponse
		err := connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
			var err error
			resp, err = conn.SystemService().
				DisksService().
				DiskService(diskID).
				Get().
				Send()
			return err
		})

		if err != nil {
			if _, ok := err.(*ovirtsdk4.NotFoundError); ok {
				return nil, "", nil
			}
			return nil, "", err
		}

		return resp.MustDisk(), string(resp.MustDisk().MustStatus()), nil
	}
}

// ImageTransferStateRefreshFuncWithWrapper returns a StateRefreshFunc for image transfer status with reconnection support
func ImageTransferStateRefreshFuncWithWrapper(connWrapper *ConnectionWrapper, transferID string) StateRefreshFunc {
	return func() (interface{}, string, error) {
		var resp *ovirtsdk4.ImageTransferServiceGetResponse
		err := connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
			var err error
			resp, err = conn.SystemService().
				ImageTransfersService().
				ImageTransferService(transferID).
				Get().
				Send()
			return err
		})

		if err != nil {
			if _, ok := err.(*ovirtsdk4.NotFoundError); ok {
				return nil, "", nil
			}
			return nil, "", err
		}

		transfer := resp.MustImageTransfer()
		phase, _ := transfer.Phase()
		return transfer, string(phase), nil
	}
}
