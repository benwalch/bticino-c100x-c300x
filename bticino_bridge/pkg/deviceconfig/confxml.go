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
