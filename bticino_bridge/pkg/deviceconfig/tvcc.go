// Package deviceconfig provides parsers for BTicino device configuration files
package deviceconfig

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// TVCCSettings represents the structure of the tvcc_settings.ini file
type TVCCSettings map[string]TVCCConfig

// TVCCConfig configuration of a specific camera
type TVCCConfig struct {
	Brightness int `json:"brightness"`
	Contrast   int `json:"contrast"`
	Saturation int `json:"saturation"`
	Quality    int `json:"quality"`
}

// ReadTVCCSettings reads and parses the tvcc_settings.ini file
func ReadTVCCSettings(path string) (TVCCSettings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	content := string(data)
	lines := strings.Split(content, "\n")

	settings := make(TVCCSettings)
	currentSection := ""

	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Ignore comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Detect [TVCC XX] section
		if strings.HasPrefix(line, "[") && strings.HasPrefix(line, "[TVCC") {
			currentSection = strings.Trim(line, "[]")
			settings[currentSection] = TVCCConfig{
				Brightness: 50,
				Contrast:   50,
				Saturation: 50,
				Quality:    75,
			}
			continue
		}

		// Parse key=value
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 || currentSection == "" {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value, _ := strconv.Atoi(strings.TrimSpace(parts[1]))

		cfg := settings[currentSection]
		switch key {
		case "Brightness":
			cfg.Brightness = value
		case "Contrast":
			cfg.Contrast = value
		case "Saturation":
			cfg.Saturation = value
		case "Quality":
			cfg.Quality = value
		}
		settings[currentSection] = cfg
	}

	return settings, nil
}

// GetAllCameraIDs returns all configured camera IDs
func GetAllCameraIDs(path string) ([]string, error) {
	settings, err := ReadTVCCSettings(path)
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(settings))
	for key := range settings {
		// Extract ID from "TVCC XX"
		if len(key) > 5 {
			ids = append(ids, key[5:])
		}
	}

	return ids, nil
}

// TVCCGlobalSettings global TVCC configuration
type TVCCGlobalSettings struct {
	Settings TVCCSettings `json:"settings"`
}
