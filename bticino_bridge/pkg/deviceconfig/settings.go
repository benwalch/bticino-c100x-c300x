// Package deviceconfig provides parsers for BTicino device configuration files
package deviceconfig

import (
	"encoding/xml"
	"fmt"
	"os"
	"strconv"
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
		return nil, fmt.Errorf("parse settings.xml: %w", err)
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
		// For ringtones, id_ringtone is in <ist> on 300X but a plain <obj>
		// attribute on 100X — accept both, ist first.
		ringtoneAttr := obj.Ist.IDRingtone
		if ringtoneAttr == "" {
			ringtoneAttr = obj.IDRingtone
		}
		if ringtoneAttr != "" {
			// It is a ringtone
			ringtoneID, _ := strconv.Atoi(ringtoneAttr)
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
