// Package deviceconfig provides parsers for BTicino device configuration files
package deviceconfig

import (
	"encoding/xml"
	"fmt"
	"os"
	"strings"
)

// ConfXML represents the structure of the /var/tmp/conf.xml file
type ConfXML struct {
	Language   string `json:"language"`
	Town       string `json:"town"`
	NTPServer  string `json:"ntp_server"`
	NTPalgo    string `json:"ntp_algo"`
	DateFormat int    `json:"date_format"`
	Fuso       string `json:"fuso"`
}

// RawConfXML original XML structure for parsing
type RawConfXML struct {
	XMLName  xml.Name     `xml:"configuratore"`
	Setup    ConfSetup    `xml:"setup"`
	Generale ConfGenerale `xml:"generale"`
}

// ConfSetup structure of the setup element
type ConfSetup struct {
	Generale ConfGenerale `xml:"generale"`
}

// ConfGenerale structure of the generale element
type ConfGenerale struct {
	XMLName     xml.Name     `xml:"generale"`
	Language    string       `xml:"language"`
	Clock       ConfClock    `xml:"clock"`
	Temperature ConfTemp     `xml:"temperature"`
	Password    ConfPassword `xml:"password"`
}

// ConfClock structure of the clock element
type ConfClock struct {
	XMLName    xml.Name `xml:"clock"`
	Master     string   `xml:"master"`
	DateFormat string   `xml:"dateformat"`
	Fuso       string   `xml:"fuso"`
	Town       string   `xml:"town"`
	NTPServer  string   `xml:"ntp_server"`
	NTPAalgo   string   `xml:"ntp_algo"`
}

// ConfTemp structure of the temperature element
type ConfTemp struct {
	XMLName xml.Name `xml:"temperature"`
	Format  string   `xml:"format"`
}

// ConfPassword structure of the password element
type ConfPassword struct {
	XMLName xml.Name `xml:"password"`
	Enabled string   `xml:"enabled"`
	Pwd     string   `xml:"pwd"`
}

// ReadConfXML reads and parses the /var/tmp/conf.xml file
// Uses string parsing because the XML structure does not map correctly with standard encoding/xml
func ReadConfXML(path string) (*ConfXML, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Use string parsing for this specific XML
	return parseConfXMLSimple(string(data)), nil
}

// parseConfXMLSimple fallback using string parsing
func parseConfXMLSimple(content string) *ConfXML {
	conf := &ConfXML{}

	extract := func(tag string) string {
		startTag := "<" + tag + ">"
		endTag := "</" + tag + ">"
		if idx := strings.Index(content, startTag); idx != -1 {
			end := strings.Index(content[idx:], endTag)
			if end > 0 {
				return strings.TrimSpace(content[idx+len(startTag) : idx+end])
			}
		}
		return ""
	}

	conf.Language = extract("language")
	conf.Town = extract("town")
	conf.NTPServer = extract("ntp_server")
	conf.NTPalgo = extract("ntp_algo")
	conf.Fuso = extract("fuso")

	if df := extract("dateformat"); df != "" {
		fmt.Sscanf(df, "%d", &conf.DateFormat)
	}

	return conf
}

// GetLanguage returns the current system language
func GetLanguage(path string) (string, error) {
	conf, err := ReadConfXML(path)
	if err != nil {
		return "", err
	}
	return conf.Language, nil
}

// GetTimezone returns the configured time zone
func GetTimezone(path string) (string, error) {
	conf, err := ReadConfXML(path)
	if err != nil {
		return "", err
	}
	return conf.Town, nil
}

// GetNTPServer returns the configured NTP server
func GetNTPServer(path string) (string, error) {
	conf, err := ReadConfXML(path)
	if err != nil {
		return "", err
	}
	return conf.NTPServer, nil
}

// GetNTPConfig returns the full NTP configuration
func GetNTPConfig(path string) (server, algo string, err error) {
	conf, err := ReadConfXML(path)
	if err != nil {
		return "", "", err
	}
	return conf.NTPServer, conf.NTPalgo, nil
}

// ParseConfXMLSimple does a simple parsing of conf.xml using strings
func ParseConfXMLSimple(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := string(data)
	result := make(map[string]string)

	// Extract language
	if idx := strings.Index(content, "<language>"); idx != -1 {
		end := strings.Index(content[idx:], "</language>")
		if end > 0 {
			result["language"] = strings.TrimSpace(content[idx+len("<language>") : idx+end])
		}
	}

	// Extract town
	if idx := strings.Index(content, "<town>"); idx != -1 {
		end := strings.Index(content[idx:], "</town>")
		if end > 0 {
			result["town"] = strings.TrimSpace(content[idx+len("<town>") : idx+end])
		}
	}

	// Extract ntp_server
	if idx := strings.Index(content, "<ntp_server>"); idx != -1 {
		end := strings.Index(content[idx:], "</ntp_server>")
		if end > 0 {
			result["ntp_server"] = strings.TrimSpace(content[idx+len("<ntp_server>") : idx+end])
		}
	}

	// Extract ntp_algo
	if idx := strings.Index(content, "<ntp_algo>"); idx != -1 {
		end := strings.Index(content[idx:], "</ntp_algo>")
		if end > 0 {
			result["ntp_algo"] = strings.TrimSpace(content[idx+len("<ntp_algo>") : idx+end])
		}
	}

	// Extract dateformat
	if idx := strings.Index(content, "<dateformat>"); idx != -1 {
		end := strings.Index(content[idx:], "</dateformat>")
		if end > 0 {
			result["dateformat"] = strings.TrimSpace(content[idx+len("<dateformat>") : idx+end])
		}
	}

	// Extract fuso
	if idx := strings.Index(content, "<fuso>"); idx != -1 {
		end := strings.Index(content[idx:], "</fuso>")
		if end > 0 {
			result["fuso"] = strings.TrimSpace(content[idx+len("<fuso>") : idx+end])
		}
	}

	// Extract language from general
	if result["language"] == "" {
		if idx := strings.Index(content, "<language>"); idx != -1 {
			result["language"] = extractValue(content, idx, "<language>", "</language>")
		}
	}

	return result, nil
}

// Helper to extract simple values
func extractValue(content string, startIdx int, openTag, closeTag string) string {
	end := strings.Index(content[startIdx:], closeTag)
	if end > 0 {
		return strings.TrimSpace(content[startIdx+len(openTag) : startIdx+end])
	}
	return ""
}

// GetDeviceInfoFromConfXML extracts device information from conf.xml
func GetDeviceInfoFromConfXML(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := string(data)
	result := make(map[string]string)

	// Model
	if idx := strings.Index(content, "<modello>"); idx != -1 {
		result["model"] = extractValue(content, idx, "<modello>", "</modello>")
	}

	// Name
	if idx := strings.Index(content, "<nome>"); idx != -1 {
		result["name"] = extractValue(content, idx, "<nome>", "</nome>")
	}

	// IP (ethernet/lan/addressip)
	if idx := strings.Index(content, "<addressip>"); idx != -1 {
		result["ip"] = extractValue(content, idx, "<addressip>", "</addressip>")
	}

	// Netmask
	if idx := strings.Index(content, "<netmask>"); idx != -1 {
		result["netmask"] = extractValue(content, idx, "<netmask>", "</netmask>")
	}

	// Router
	if idx := strings.Index(content, "<router>"); idx != -1 {
		result["router"] = extractValue(content, idx, "<router>", "</router>")
	}

	// Device ID (mhe/hppp/deviceID)
	if idx := strings.Index(content, "<deviceID>"); idx != -1 {
		result["device_id"] = extractValue(content, idx, "<deviceID>", "</deviceID>")
	}

	// Version (ver_xml)
	if idx := strings.Index(content, "<ver_xml>"); idx != -1 {
		result["config_version"] = extractValue(content, idx, "<ver_xml>", "</ver_xml>")
	}

	return result, nil
}
