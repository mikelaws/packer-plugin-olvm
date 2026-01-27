package olvm

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// parseQcow2VirtualSize reads the virtual disk size from a qcow2 file header
// The virtual size is stored at bytes 24-31 as a 64-bit big-endian integer
func parseQcow2VirtualSize(filePath string) (int64, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("Error opening qcow2 file: %s", err)
	}
	defer file.Close()

	// Read the magic bytes to verify it's a qcow2 file
	magic := make([]byte, 4)
	if _, err := file.Read(magic); err != nil {
		return 0, fmt.Errorf("Error reading qcow2 magic: %s", err)
	}

	// Check magic: "QFI\xfb"
	if string(magic) != "QFI\xfb" {
		return 0, fmt.Errorf("Invalid qcow2 magic: expected QFI\\xfb, got %q", magic)
	}

	// Skip to byte 24 where virtual size is stored
	// Bytes 0-3: magic (already read)
	// Bytes 4-7: version
	// Bytes 8-11: backing_file_offset
	// Bytes 12-15: backing_file_size
	// Bytes 16-19: cluster_bits
	// Bytes 20-23: size (deprecated, always 0)
	// Bytes 24-31: virtual_size (what we need)
	if _, err := file.Seek(24, io.SeekStart); err != nil {
		return 0, fmt.Errorf("Error seeking to virtual size field: %s", err)
	}

	// Read 8 bytes (64-bit big-endian integer)
	var virtualSize uint64
	if err := binary.Read(file, binary.BigEndian, &virtualSize); err != nil {
		return 0, fmt.Errorf("Error reading virtual size: %s", err)
	}

	return int64(virtualSize), nil
}

// detectImageFormat detects the actual format of an image file by reading its header
// Returns "qcow2", "raw", or "unknown"
func detectImageFormat(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "unknown", fmt.Errorf("Error opening file: %s", err)
	}
	defer file.Close()

	// Read the first 4 bytes to check for QCOW2 magic
	magic := make([]byte, 4)
	if _, err := file.Read(magic); err != nil {
		return "unknown", fmt.Errorf("Error reading file header: %s", err)
	}

	// Check for QCOW2 magic: "QFI\xfb"
	if string(magic) == "QFI\xfb" {
		return "qcow2", nil
	}

	// If not QCOW2, assume RAW (RAW files don't have a standard header)
	// Other formats would need additional detection logic
	return "raw", nil
}
