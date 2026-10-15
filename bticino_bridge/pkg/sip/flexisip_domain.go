package sip

import (
	"fmt"
	"os"
	"strings"
)

// flexisipConfPaths holds the live config first, the staged copy second.
var flexisipConfPaths = []string{
	"/etc/flexisip/flexisip.conf",
	"/home/bticino/cfg/extra/90/flexisip/flexisip.conf",
}

// ReadFlexisipDomain returns the SIP domain from the local Flexisip config
// (aliases=, cross-checked against reg-domains=) plus the file it came from.
func ReadFlexisipDomain() (string, string, error) {
	for _, path := range flexisipConfPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		value, regDomains := parseFlexisipConf(string(data))
		if value == "" {
			continue
		}
		if regDomains != "" && regDomains != value {
			return "", "", fmt.Errorf("aliases=%s disagrees with reg-domains=%s in %s", value, regDomains, path)
		}
		return value, path, nil
	}
	return "", "", fmt.Errorf("no Flexisip config with aliases= found")
}

func parseFlexisipConf(data string) (aliases, regDomains string) {
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "aliases=") && aliases == "" {
			if first, _, _ := strings.Cut(strings.TrimPrefix(line, "aliases="), ","); first != "" {
				aliases = strings.TrimSpace(first)
			}
		}
		if strings.HasPrefix(line, "reg-domains=") && regDomains == "" {
			regDomains = strings.TrimSpace(strings.TrimPrefix(line, "reg-domains="))
		}
	}
	return aliases, regDomains
}
