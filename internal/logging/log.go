package logging

import (
	"fmt"
	"sort"
	"strings"
)

// Log prints a structured line: [MODULE] action=... key=value ...
func Log(module, action string, fields map[string]string) {
	if module == "" {
		module = "APP"
	}
	parts := []string{}
	if action != "" {
		parts = append(parts, "action="+action)
	}
	if len(fields) > 0 {
		keys := make([]string, 0, len(fields))
		for k := range fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			val := strings.ReplaceAll(fields[k], " ", "_")
			parts = append(parts, k+"="+val)
		}
	}
	if len(parts) == 0 {
		fmt.Printf("[%s]\n", module)
		return
	}
	fmt.Printf("[%s] %s\n", module, strings.Join(parts, " "))
}
