//go:build windows

package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// populatePluginSettingsMetadata performs a bounded, refresh-time scan for
// editable INI files near the DLL. The UI never touches the filesystem while
// painting. Reserved WRSR/Workshop metadata is excluded so "Open settings"
// does not accidentally open workshopconfig.ini.
func populatePluginSettingsMetadata(p *Plugin) {
	if p == nil || strings.TrimSpace(p.DLLPath) == "" {
		return
	}
	dllDir := filepath.Dir(p.DLLPath)
	dirs := []string{dllDir}
	base := strings.ToLower(filepath.Base(dllDir))
	if base == "plugin" || base == "plugins" {
		parent := filepath.Dir(dllDir)
		if parent != dllDir {
			dirs = append(dirs, parent)
		}
	}

	type candidate struct {
		path string
		rank int
	}
	var candidates []candidate
	seen := map[string]bool{}
	dllStem := strings.TrimSuffix(filepath.Base(p.DLLPath), filepath.Ext(p.DLLPath))
	exactDLL := strings.ToLower(dllStem + ".ini")
	exactID := strings.ToLower(p.Manifest.ID + ".ini")
	exactName := strings.ToLower(strings.TrimSpace(p.Manifest.Name) + ".ini")

	for dirIndex, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".ini") {
				continue
			}
			lower := strings.ToLower(entry.Name())
			if lower == "workshopconfig.ini" || lower == "tesmioloader.ini" {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			key := strings.ToLower(filepath.Clean(path))
			if seen[key] {
				continue
			}
			seen[key] = true
			rank := 100 + dirIndex
			switch lower {
			case exactDLL:
				rank = 0
			case exactID:
				rank = 1
			case exactName:
				rank = 2
			}
			candidates = append(candidates, candidate{path: path, rank: rank})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].rank != candidates[j].rank {
			return candidates[i].rank < candidates[j].rank
		}
		return strings.ToLower(candidates[i].path) < strings.ToLower(candidates[j].path)
	})
	p.SettingsDir = dllDir
	p.SettingsCount = len(candidates)
	if len(candidates) == 0 {
		return
	}
	commonDir := filepath.Dir(candidates[0].path)
	for _, c := range candidates[1:] {
		if !strings.EqualFold(filepath.Dir(c.path), commonDir) {
			commonDir = dllDir
			break
		}
	}
	p.SettingsDir = commonDir
	// Exact-name matches are deterministic even if other INIs are present.
	if candidates[0].rank <= 2 {
		p.SettingsPath = candidates[0].path
		return
	}
	// If there is only one non-reserved INI nearby, it is safe and useful to
	// treat it as the plugin's settings file. Multiple generic INIs are left
	// ambiguous; the UI opens the folder instead of guessing.
	if len(candidates) == 1 {
		p.SettingsPath = candidates[0].path
	}
}
