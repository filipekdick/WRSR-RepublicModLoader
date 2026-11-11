package main

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// SemanticDefinition is intentionally small. It records only game functions whose
// identity is supported by decompile/caller evidence across known WRSR builds.
type SemanticDefinition struct {
	ID          string
	DisplayName string
	Category    string
	Temperature string
	Evidence    []string
	KnownRVAs   map[string]uint32 // exact SOVIET64 SHA256 -> function entry RVA
}

const (
	wrsrSHA11113 = "4df5fbecc75300e3b843ae4e6426761c84ae2e989321c011c6f560be79122125"
	wrsrSHA1117  = "ec05bb6257da31cfcaec639c8462683ee7bcf26158e0751a29a2d48025169522"
	wrsrSHA1119  = "296644a9f207d609031fc2ae73fed2dcb34619a1d55a35d1c7b51965ce6841b8"
)

var semanticDefinitions = []SemanticDefinition{
	{
		ID: "wrsr.resource_get", DisplayName: "ResourceGet", Category: "resource",
		Temperature: "EXTREME",
		Evidence:    []string{"unique ResourceGet error-string anchor", "containing runtime function", "known-build decompile cross-check"},
		KnownRVAs:   map[string]uint32{wrsrSHA1117: 0x2AA7C0, wrsrSHA1119: 0x2AA830, wrsrSHA11113: 0x2AA7B0},
	},
	{
		ID: "wrsr.construction.tool_frame", DisplayName: "Construction / Tool Interaction Frame", Category: "construction",
		Temperature: "HOT",
		Evidence:    []string{"C3D_INPUT::GetMouseSolid interaction frame", "terrain/water collision and construction-tool command dispatch", "cross-build decompile identity"},
		KnownRVAs:   map[string]uint32{wrsrSHA1117: 0x2F0E70, wrsrSHA1119: 0x2F0F10, wrsrSHA11113: 0x2F0E90},
	},
	{
		ID: "wrsr.building.assignment_target_select", DisplayName: "Building Assignment Target Selection", Category: "building",
		Temperature: "WARM",
		Evidence:    []string{"C3D_BUILDING assignment/selection path", "gui_building_assign_ok/fail behaviour", "cross-build decompile identity"},
		KnownRVAs:   map[string]uint32{wrsrSHA1117: 0x2B7840, wrsrSHA1119: 0x2B78B0},
	},
	{
		ID: "wrsr.vehicle.compatibility_check", DisplayName: "Vehicle Compatibility Check", Category: "vehicle",
		Temperature: "WARM",
		Evidence:    []string{"vehicles ResourceGet path", "trainbox/tram compatibility decisions", "cross-build decompile identity"},
		KnownRVAs:   map[string]uint32{wrsrSHA1117: 0x3E2860, wrsrSHA1119: 0x3E2900},
	},
	{
		ID: "wrsr.vehicle.task_advance", DisplayName: "Vehicle Task Advance", Category: "vehicle",
		Temperature: "WARM",
		Evidence:    []string{"task index increment/bounds handling", "next-task progression logic", "cross-build decompile identity"},
		KnownRVAs:   map[string]uint32{wrsrSHA1117: 0x67DA00, wrsrSHA1119: 0x67DB00},
	},
	{
		ID: "wrsr.infrastructure.route_validation", DisplayName: "Infrastructure Route Validation", Category: "infrastructure",
		Temperature: "WARM",
		Evidence:    []string{"route/path node validation state", "native validation result path including 0x45A", "cross-build decompile identity"},
		KnownRVAs:   map[string]uint32{wrsrSHA1117: 0x51D090, wrsrSHA1119: 0x51D160},
	},
	{
		ID: "wrsr.gui.tool_resources_init", DisplayName: "GUI Tool Resource Initialisation", Category: "gui",
		Temperature: "COLD",
		Evidence:    []string{"editor/maintab_%s.png resource construction", "editor/tool_%s*.png resource construction", "cross-build decompile identity"},
		KnownRVAs:   map[string]uint32{wrsrSHA1117: 0x2960A0, wrsrSHA1119: 0x296110},
	},
}

func semanticDefinitionByID(id string) *SemanticDefinition {
	for i := range semanticDefinitions {
		if semanticDefinitions[i].ID == id {
			return &semanticDefinitions[i]
		}
	}
	return nil
}

func observedIDForRVA(rva uint32) string {
	return fmt.Sprintf("wrsr.observed.rva_%08x", rva)
}

func rawTargetForRVA(rva uint32) string {
	return fmt.Sprintf("SOVIET64.exe+0x%X", rva)
}

func semanticAliasMap(cache *SymbolCache) map[string]string {
	out := map[string]string{}
	if cache == nil {
		return out
	}
	for id, sym := range cache.Symbols {
		if !sym.Semantic {
			continue
		}
		out[id] = id
		out[strings.ToLower(id)] = id
		out[strings.ToLower(observedIDForRVA(sym.RVA))] = id
		out[strings.ToLower(rawTargetForRVA(sym.RVA))] = id
		for _, alias := range sym.Aliases {
			if strings.TrimSpace(alias) != "" {
				out[strings.ToLower(strings.TrimSpace(alias))] = id
			}
		}
	}
	return out
}

func canonicalTarget(cache *SymbolCache, target string) string {
	t := strings.TrimSpace(target)
	if t == "" {
		return ""
	}
	aliases := semanticAliasMap(cache)
	if id := aliases[strings.ToLower(t)]; id != "" {
		return id
	}
	return t
}

func runtimeExtentForEntry(layout *peLayout, entry uint32) (uint32, uint32, bool) {
	funcs := runtimeFunctionCandidates(layout)
	lo, hi := 0, len(funcs)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if funcs[mid].Begin <= entry {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return 0, 0, false
	}
	fn := funcs[lo-1]
	if entry < fn.Begin || entry >= fn.End {
		return 0, 0, false
	}
	return fn.Begin, fn.End, true
}

// promoteKnownSemanticSymbols adds only exact-build semantic identities supported
// by the decompiled 1.1.1.7 / 1.1.1.9 reference set. Existing OBSERVED records
// are deliberately retained; they become aliases/evidence, not discarded data.
func promoteKnownSemanticSymbols(game *GameIdentity, cache *SymbolCache) (bool, error) {
	if game == nil || game.Path == "" || game.SHA256 == "" || cache == nil {
		return false, nil
	}
	layout, err := parsePELayout(game.Path)
	if err != nil {
		return false, err
	}
	if cache.Symbols == nil {
		cache.Symbols = map[string]CachedSymbol{}
	}
	changed := false
	for _, def := range semanticDefinitions {
		rva, ok := def.KnownRVAs[game.SHA256]
		if !ok || rva == 0 {
			continue
		}
		off, ok := layout.rvaToOffset(rva)
		if !ok || off < 0 || off+16 > len(layout.Data) {
			continue
		}
		entry := strings.ToUpper(hex.EncodeToString(layout.Data[off : off+16]))
		begin, end, haveRuntime := runtimeExtentForEntry(layout, rva)
		runtimeSize := 0
		if haveRuntime && begin == rva && end > begin {
			runtimeSize = int(end - begin)
		}
		existing := cache.Symbols[def.ID]
		aliases := append([]string{}, existing.Aliases...)
		aliases = append(aliases, observedIDForRVA(rva), rawTargetForRVA(rva))
		sort.Strings(aliases)
		aliases = uniqueStrings(aliases)
		rec := CachedSymbol{
			ID: def.ID, RVA: rva, FunctionSize: runtimeSize, Confidence: "CONFIRMED",
			Resolver: "semantic catalogue exact-build mapping + current entry validation", EntryFingerprint: entry,
			Semantic: true, DisplayName: def.DisplayName, Aliases: aliases, Temperature: def.Temperature}
		if fmt.Sprintf("%+v", existing) != fmt.Sprintf("%+v", rec) {
			changed = true
		}
		cache.Symbols[def.ID] = rec
	}
	return changed, nil
}
