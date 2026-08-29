package main

import (
	"fmt"
	"log"

	php "github.com/IamYGT/heyserver/internal/services/php"
)

func main() {
	s := php.New()

	// --- Test GetGlobalINI ---
	ini, err := s.GetGlobalINI("8.4")
	if err != nil {
		log.Fatalf("GetGlobalINI: %v", err)
	}
	fmt.Printf("GetGlobalINI: %d directives, memory_limit=%s, upload_max_filesize=%s\n",
		len(ini), ini["memory_limit"], ini["upload_max_filesize"])

	// --- Test GetINIDiff ---
	diffs, err := s.GetINIDiff("8.4")
	if err != nil {
		log.Fatalf("GetINIDiff: %v", err)
	}
	fmt.Printf("GetINIDiff: %d differences from defaults\n", len(diffs))
	for i, d := range diffs {
		if i >= 5 {
			break
		}
		fmt.Printf("  %s: current=%q default=%q\n", d.Key, d.Current, d.Default)
	}

	// --- Test ListINIDirectives ---
	dirs, err := s.ListINIDirectives("8.4")
	if err != nil {
		log.Fatalf("ListINIDirectives: %v", err)
	}
	fmt.Printf("ListINIDirectives: %d directives total\n", len(dirs))
	for _, d := range dirs {
		if d.Section == "Resource Limits" {
			fmt.Printf("  [%s] %s = %s (default: %s, type: %s)\n",
				d.Section, d.Key, d.Value, d.DefaultValue, d.Type)
		}
	}

	// --- Test ListExtensions ---
	exts, err := s.ListExtensions("8.4")
	if err != nil {
		log.Fatalf("ListExtensions: %v", err)
	}
	enabled := 0
	for _, e := range exts {
		if e.Enabled {
			enabled++
		}
	}
	fmt.Printf("ListExtensions: %d total, %d enabled\n", len(exts), enabled)
	for _, e := range exts {
		if e.Name == "redis" || e.Name == "imagick" || e.Name == "opcache" {
			fmt.Printf("  %s: enabled=%v type=%s version=%s ini=%s\n",
				e.Name, e.Enabled, e.Type, e.Version, e.INIFile)
		}
	}

	// --- Test GetDomainINI ---
	domINI, err := s.GetDomainINI("8.4", "api.example.com")
	if err != nil {
		log.Fatalf("GetDomainINI: %v", err)
	}
	fmt.Printf("GetDomainINI api.example.com: %d overrides\n", len(domINI))
	for k, v := range domINI {
		fmt.Printf("  %s = %s\n", k, v)
	}

	// --- Test SearchAvailableExtensions ---
	results, err := s.SearchAvailableExtensions("8.4", "amqp")
	if err != nil {
		log.Fatalf("SearchAvailableExtensions: %v", err)
	}
	fmt.Printf("SearchAvailableExtensions(8.4, amqp): %v\n", results)
}
