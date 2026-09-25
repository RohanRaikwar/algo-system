package config

import "strings"

// ExchangeTypeName maps SmartAPI exchange type codes to exchange names.
// Returns (name, true) for known codes, and ("NSE", false) for unknown codes.
func ExchangeTypeName(code string) (string, bool) {
	switch strings.TrimSpace(code) {
	case "1":
		return "NSE", true
	case "2":
		return "NFO", true
	case "3":
		return "BSE", true
	case "4":
		return "BSE_FO", true
	case "5":
		return "MCX_FO", true
	case "7":
		return "NCX_FO", true
	case "13":
		return "CDE_FO", true
	default:
		return "NSE", false
	}
}

// ParseSubscribeTokenKeys parses "exchangeType:token,..." into
// "exchange:token" keys used across services.
func ParseSubscribeTokenKeys(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}

	var keys []string
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 {
			continue
		}
		exName, _ := ExchangeTypeName(parts[0])
		token := strings.TrimSpace(parts[1])
		if token == "" {
			continue
		}
		keys = append(keys, exName+":"+token)
	}
	return keys
}
