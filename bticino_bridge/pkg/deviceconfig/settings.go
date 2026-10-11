// Package deviceconfig provides parsers for BTicino device configuration files
package deviceconfig

import (
	"encoding/xml"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// SettingsXML represents the structure of the settings.xml file (directory 0)
type SettingsXML struct {
	Ringtones Ringtones  `json:"ringtones"`
	Volumes   Volumes    `json:"volumes"`
	Display   Display    `json:"display"`
	Extra     []ExtraObj `json:"extra"`
}

// ExtraObj represents additional objects in settings.xml
type ExtraObj struct {
	CID        string `json:"cid"`
	ID         string `json:"id"`
	Enable     *int   `json:"enable,omitempty"`
	Volume     *int   `json:"volume,omitempty"`
	Brightness *int   `json:"brightness,omitempty"`
	Color      *int   `json:"color,omitempty"`
	Contrast   *int   `json:"contrast,omitempty"`
	CleanTime  *int   `json:"clean_time,omitempty"`
	SourceType *int   `json:"source_type,omitempty"`
	Days       *int   `json:"days,omitempty"`
	Minutes    *int   `json:"minutes,omitempty"`
	Hour       *int   `json:"hour,omitempty"`
	Enabled    *int   `json:"enabled,omitempty"`
	Mode       *int   `json:"mode,omitempty"`
}

// RawSettingsXML original XML structure
type RawSettingsXML struct {
	XMLName xml.Name         `xml:"settings"`
	Objects []RawSettingsObj `xml:"obj"`
}

// RawSettingsObj represents an object in the XML
type RawSettingsObj struct {
	CID                 string         `xml:"cid,attr"`
	ID                  string         `xml:"id,attr"`
	Descr               string         `xml:"descr,attr"`
	IDRingtone          string         `xml:"id_ringtone,attr"`
	Enable              string         `xml:"enable,attr"`
	Volume              string         `xml:"volume,attr"`
	Brightness          string         `xml:"brightness,attr"`
	Color               string         `xml:"color,attr"`
	Contrast            string         `xml:"contrast,attr"`
	CleanTime           string         `xml:"clean_time,attr"`
	Type                string         `xml:"type,attr"`
	SourceType          string         `xml:"source_type,attr"`
	Days                string         `xml:"days,attr"`
	Minutes             string         `xml:"minutes,attr"`
	Hour                string         `xml:"hour,attr"`
	Enabled             string         `xml:"enabled,attr"`
	Mode                string         `xml:"mode,attr"`
	ConfigurationActive string         `xml:"configuration_active,attr"`
	Ist                 RawSettingsIst `xml:"ist"`
}

// RawSettingsIst represents the ist element inside an object
type RawSettingsIst struct {
	UII                 string `xml:"uii,attr"`
	IDRingtone          string `xml:"id_ringtone,attr"`
	Enable              string `xml:"enable,attr"`
	Volume              string `xml:"volume,attr"`
	Brightness          string `xml:"brightness,attr"`
	Color               string `xml:"color,attr"`
	Contrast            string `xml:"contrast,attr"`
	ConfigurationActive string `xml:"configuration_active,attr"`
}

// ReadSettingsXML reads and parses the settings.xml file
func ReadSettingsXML(path string) (*SettingsXML, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var raw RawSettingsXML
	if err := xml.Unmarshal(data, &raw); err != nil {
		return ReadSettingsXMLSimple(path)
	}

	settings := &SettingsXML{
		Ringtones: Ringtones{},
		Volumes:   Volumes{},
		Display:   Display{},
		Extra:     []ExtraObj{},
	}

	// Process each object
	for _, obj := range raw.Objects {
		// Determine type based on attributes
		// For ringtones, id_ringtone is in <ist>, not in <obj>
		if obj.Ist.IDRingtone != "" {
			// It is a ringtone
			ringtoneID, _ := strconv.Atoi(obj.Ist.IDRingtone)
			if obj.Ist.UII != "" {
				uii, _ := strconv.Atoi(obj.Ist.UII)

				assignRingtone(&settings.Ringtones, uii, ringtoneID)
			}
		} else if obj.CID == "14121" || obj.CID == "14122" || obj.CID == "14123" ||
			obj.CID == "14124" || obj.CID == "14125" || obj.CID == "14127" || obj.CID == "14129" {
			// It is a volume
			volume := 50
			if obj.Ist.Volume != "" {
				volume, _ = strconv.Atoi(obj.Ist.Volume)
			} else if obj.Volume != "" {
				volume, _ = strconv.Atoi(obj.Volume)
			}
			assignVolume(&settings.Volumes, obj.CID, volume)
		} else if obj.CID == "14151" {
			// Display brightness
			brightness := 100
			if obj.Ist.Brightness != "" {
				brightness, _ = strconv.Atoi(obj.Ist.Brightness)
			} else if obj.Brightness != "" {
				brightness, _ = strconv.Atoi(obj.Brightness)
			}
			settings.Display.Brightness = brightness
		} else if obj.CID == "14152" {
			// Clean time
			cleanTime := 10000
			if obj.CleanTime != "" {
				cleanTime, _ = strconv.Atoi(obj.CleanTime)
			}
			settings.Display.CleanTime = cleanTime
		} else {
			// Additional object
			extra := ExtraObj{CID: obj.CID, ID: obj.ID}
			if obj.Enable != "" {
				v, _ := strconv.Atoi(obj.Enable)
				extra.Enable = &v
			}
			if obj.Ist.Enable != "" {
				v, _ := strconv.Atoi(obj.Ist.Enable)
				extra.Enable = &v
			}
			if obj.Volume != "" {
				v, _ := strconv.Atoi(obj.Volume)
				extra.Volume = &v
			}
			if obj.Ist.Volume != "" {
				v, _ := strconv.Atoi(obj.Ist.Volume)
				extra.Volume = &v
			}
			if obj.Brightness != "" {
				v, _ := strconv.Atoi(obj.Brightness)
				extra.Brightness = &v
			}
			if obj.Ist.Brightness != "" {
				v, _ := strconv.Atoi(obj.Ist.Brightness)
				extra.Brightness = &v
			}
			settings.Extra = append(settings.Extra, extra)
		}
	}

	return settings, nil
}

// assignRingtone assigns the ringtone by UII
func assignRingtone(r *Ringtones, uii, ringtoneID int) {
	switch uii {
	case 52: // Ringtone S0
		r.S0 = ringtoneID
	case 53: // Ringtone S1
		r.S1 = ringtoneID
	case 54: // Ringtone S2
		r.S2 = ringtoneID
	case 55: // Ringtone S3
		r.S3 = ringtoneID
	case 56: // Ringtone Internal
		r.Internal = ringtoneID
	case 57: // Ringtone External
		r.External = ringtoneID
	case 58: // Ringtone Door
		r.Door = ringtoneID
	case 59: // Ringtone Alarm
		r.Alarm = ringtoneID
	case 60: // Ringtone Message
		r.Message = ringtoneID
	}
}

// assignVolume assigns the volume by CID
func assignVolume(v *Volumes, cid string, volume int) {
	switch cid {
	case "14121":
		v.S0 = volume
	case "14122":
		v.S1 = volume
	case "14123":
		v.S2 = volume
	case "14124":
		v.Intercom = volume
	case "14127":
		v.Door = volume
	case "14129":
		v.SIP = volume
	}
}

// ReadSettingsXMLSimple does a simple parsing using strings
func ReadSettingsXMLSimple(path string) (*SettingsXML, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	content := string(data)
	settings := &SettingsXML{
		Ringtones: Ringtones{},
		Volumes:   Volumes{},
		Display:   Display{},
	}

	// Find all obj elements with id_ringtone
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "id_ringtone=") && !strings.Contains(line, "cid=\"141") {
			continue
		}

		// Extract cid
		cidStart := strings.Index(line, "cid=\"")
		if cidStart == -1 {
			continue
		}
		cidEnd := strings.Index(line[cidStart:], "\"")
		if cidEnd == -1 {
			continue
		}
		cid := line[cidStart+5 : cidStart+cidEnd]

		// Extract id_ringtone
		ringStart := strings.Index(line, "id_ringtone=\"")
		var ringtoneID int
		if ringStart != -1 {
			ringEnd := strings.Index(line[ringStart:], "\"")
			if ringEnd != -1 {
				ringtoneID, _ = strconv.Atoi(line[ringStart+13 : ringStart+ringEnd])
			}
		}

		// Extract uii from the ist element
		uiiStart := strings.Index(line, "uii=\"")
		var uii int
		if uiiStart != -1 {
			uiiEnd := strings.Index(line[uiiStart:], "\"")
			if uiiEnd != -1 {
				uii, _ = strconv.Atoi(line[uiiStart+5 : uiiStart+uiiEnd])
			}
		}

		// Assign by UII
		if uii > 0 && ringtoneID > 0 {
			assignRingtone(&settings.Ringtones, uii, ringtoneID)
		}

		// Find volume
		volStart := strings.Index(line, "volume=\"")
		if volStart != -1 {
			volEnd := strings.Index(line[volStart:], "\"")
			if volEnd != -1 {
				volume, _ := strconv.Atoi(line[volStart+8 : volStart+volEnd])
				assignVolume(&settings.Volumes, cid, volume)
			}
		}

		// Find brightness
		if cid == "14151" {
			brightStart := strings.Index(line, "brightness=\"")
			if brightStart != -1 {
				brightEnd := strings.Index(line[brightStart:], "\"")
				if brightEnd != -1 {
					settings.Display.Brightness, _ = strconv.Atoi(line[brightStart+12 : brightStart+brightEnd])
				}
			}
		}
	}

	return settings, nil
}

// GetRingtone returns the ringtone for a specific UII
func GetRingtone(path string, uii int) (int, error) {
	settings, err := ReadSettingsXML(path)
	if err != nil {
		return 0, err
	}

	switch uii {
	case 52:
		return settings.Ringtones.S0, nil
	case 53:
		return settings.Ringtones.S1, nil
	case 54:
		return settings.Ringtones.S2, nil
	case 55:
		return settings.Ringtones.S3, nil
	case 56:
		return settings.Ringtones.Internal, nil
	case 57:
		return settings.Ringtones.External, nil
	case 58:
		return settings.Ringtones.Door, nil
	case 59:
		return settings.Ringtones.Alarm, nil
	case 60:
		return settings.Ringtones.Message, nil
	}

	return 0, fmt.Errorf("unknown uii: %d", uii)
}

// GetVolume returns the volume for a specific CID
func GetVolume(path string, cid string) (int, error) {
	settings, err := ReadSettingsXML(path)
	if err != nil {
		return 0, err
	}

	switch cid {
	case "14121":
		return settings.Volumes.S0, nil
	case "14122":
		return settings.Volumes.S1, nil
	case "14123":
		return settings.Volumes.S2, nil
	case "14124":
		return settings.Volumes.Intercom, nil
	case "14127":
		return settings.Volumes.Door, nil
	case "14129":
		return settings.Volumes.SIP, nil
	}

	return 0, fmt.Errorf("unknown cid: %s", cid)
}

// GetDisplayBrightness returns the display brightness
func GetDisplayBrightness(path string) (int, error) {
	settings, err := ReadSettingsXML(path)
	if err != nil {
		return 0, err
	}
	return settings.Display.Brightness, nil
}

// GetDisplayCleanTime returns the display clean time
func GetDisplayCleanTime(path string) (int, error) {
	settings, err := ReadSettingsXML(path)
	if err != nil {
		return 0, err
	}
	return settings.Display.CleanTime, nil
}
