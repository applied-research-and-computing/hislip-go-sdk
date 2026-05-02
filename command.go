package hislip

import (
	"fmt"
	"strings"
)

// SCPICommand is a parsed SCPI command passed to handlers.
//
// Handlers receive an SCPICommand rather than a raw string. It exposes the full
// command text, the matched handler prefix, whether it is a query, and
// comma-separated arguments with type-safe accessors.
type SCPICommand struct {
	Raw    string   // Full command string, e.g. "CONF:VOLT:DC 10,0.001"
	Prefix string   // Matched handler prefix, e.g. "CONF:VOLT:DC"
	Query  bool     // True if the command ends with '?'
	Args   []string // Comma-separated arguments after the prefix, whitespace-stripped
}

func newSCPICommand(raw, prefix string) *SCPICommand {
	cmd := &SCPICommand{
		Raw:    raw,
		Prefix: prefix,
		Query:  strings.HasSuffix(strings.TrimRight(raw, " \t\r\n"), "?"),
	}
	remainder := strings.TrimSpace(raw[len(prefix):])
	if remainder != "" {
		parts := strings.Split(remainder, ",")
		for i, p := range parts {
			parts[i] = strings.TrimSpace(p)
		}
		cmd.Args = parts
	}
	return cmd
}

// ArgString returns the argument at index as a string.
// Returns defaultVal if the index is out of range.
func (c *SCPICommand) ArgString(index int, defaultVal string) string {
	if index >= len(c.Args) {
		return defaultVal
	}
	return c.Args[index]
}

// ArgFloat returns the argument at index parsed as float64.
// Returns defaultVal if the argument is missing or cannot be parsed.
func (c *SCPICommand) ArgFloat(index int, defaultVal float64) float64 {
	if index >= len(c.Args) {
		return defaultVal
	}
	var v float64
	if _, err := fmt.Sscanf(c.Args[index], "%g", &v); err != nil {
		return defaultVal
	}
	return v
}

// ArgInt returns the argument at index parsed as int.
// Returns defaultVal if the argument is missing or cannot be parsed.
func (c *SCPICommand) ArgInt(index int, defaultVal int) int {
	if index >= len(c.Args) {
		return defaultVal
	}
	var iv int
	if _, err := fmt.Sscanf(c.Args[index], "%d", &iv); err == nil {
		return iv
	}
	var fv float64
	if _, err := fmt.Sscanf(c.Args[index], "%g", &fv); err == nil {
		return int(fv)
	}
	return defaultVal
}

func (c *SCPICommand) String() string {
	return c.Raw
}
