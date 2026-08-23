package main

import (
	"bytes"
	"debug/pe"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type TargetInteraction struct {
	Symbol      string `json:"symbol"`
	Interaction string `json:"interaction"`
	Phase       string `json:"phase,omitempty"`
	Evidence    string `json:"evidence"`
	Confidence  string `json:"confidence"`
	Managed     bool   `json:"managed"`
	Temperature string `json:"temperature"`
}

type PluginAnalysis struct {
	Path             string              `json:"path"`
	Filename         string              `json:"filename"`
	SHA256           string              `json:"sha256"`
	Size             int64               `json:"size"`
	Machine          string              `json:"machine"`
	PE64             bool                `json:"pe64"`
	LoaderType       string              `json:"loaderType"`
	PluginID         string              `json:"pluginId,omitempty"`
	WorkshopID       string              `json:"workshopId,omitempty"`
	Name             string              `json:"name"`
	Version          string              `json:"version,omitempty"`
	Author           string              `json:"author,omitempty"`
	Enabled          bool                `json:"enabled"`
	Priority         int                 `json:"priority,omitempty"`
	LoadOrder        int                 `json:"loadOrder,omitempty"`
	Imports          []string            `json:"imports,omitempty"`
	Exports          []string            `json:"exports,omitempty"`
	HookingSignals   []string            `json:"hookingSignals,omitempty"`
	DeclaredTargets  []string            `json:"declaredTargets,omitempty"`
	Interactions     []TargetInteraction `json:"interactions,omitempty"`
	Footprint        string              `json:"footprint"`
	Compatibility    string              `json:"compatibility"`
	CompatibilityWhy string              `json:"compatibilityWhy"`
	ProfileState     string              `json:"profileState"`
}

type CompatibilityTargetInteraction struct {
	PluginID   string `json:"pluginId,omitempty"`
	PluginName string `json:"pluginName"`
	LoaderType string `json:"loaderType"`
	Action     string `json:"action"`
	Phase      string `json:"phase,omitempty"`
	Evidence   string `json:"evidence"`
	Managed    bool   `json:"managed"`
}

type CompatibilityChainStep struct {
	PluginID       string `json:"pluginId"`
	PluginSHA256   string `json:"pluginSha256,omitempty"`
	Label          string `json:"label,omitempty"`
	ExecutionOrder int    `json:"executionOrder"`
}

type CompatibilityTarget struct {
	Symbol       string                           `json:"symbol"`
	DisplayName  string                           `json:"displayName,omitempty"`
	Temperature  string                           `json:"temperature"`
	Result       string                           `json:"result"`
	Reason       string                           `json:"reason"`
	Proof        string                           `json:"proof,omitempty"`
	Chain        []CompatibilityChainStep         `json:"chain,omitempty"`
	Interactions []CompatibilityTargetInteraction `json:"interactions"`
}

type CompatibilitySummary struct {
	DLLCount            int                   `json:"detectedDlls"`
	SelectedDLLs        int                   `json:"enabledDlls"`
	RMLNative           int                   `json:"rmlNative"`
	TesmioLegacy        int                   `json:"tesmioLegacy"`
	UnknownNative       int                   `json:"unknownNative"`
	KnownTargets        int                   `json:"knownTargets"`
	SharedTargets       int                   `json:"sharedTargets"`
	ManagedShared       int                   `json:"managedShared"`
	PotentialConflicts  int                   `json:"potentialConflicts"`
	ProvenHealthyShared int                   `json:"provenHealthyShared"`
	UnprovenShared      int                   `json:"unprovenShared"`
	RecognizedExternal  int                   `json:"recognizedExternalChains"`
	OrderSensitive      int                   `json:"orderSensitive"`
	LifecycleFailures   int                   `json:"lifecycleFailures"`
	UnmanagedChanges    int                   `json:"unmanagedCodeChanges"`
	BridgeCandidates    int                   `json:"bridgeCandidates"`
	UnknownProfiles     int                   `json:"unknownProfiles"`
	Overall             string                `json:"overall"`
	OverallClass        int                   `json:"overallClass"`
	Targets             []CompatibilityTarget `json:"targets"`
}

type CompatibilityReport struct {
	SchemaVersion int                  `json:"schemaVersion"`
	GeneratedAt   string               `json:"generatedAt"`
	Summary       CompatibilitySummary `json:"summary"`
	DLLs          []PluginAnalysis     `json:"dlls"`
	Warnings      []string             `json:"warnings,omitempty"`
}

func analyseCompatibility(base, knowledgeRoot, pluginDir string, manifests []*Plugin, index *SymbolCache, runtimeReport *RuntimeReport) (CompatibilitySummary, []string) {
	var analyses []PluginAnalysis
	var warnings []string
	seen := map[string]bool{}
	roots := map[string]bool{}
	if pluginDir != "" {
		roots[filepath.Clean(pluginDir)] = true
	}
	for _, plugin := range manifests {
		if plugin == nil || plugin.DLLPath == "" {
			continue
		}
		if plugin.SourceRoot != "" {
			roots[filepath.Clean(plugin.SourceRoot)] = true
		}
		key := strings.ToLower(filepath.Clean(plugin.DLLPath))
		if seen[key] {
			continue
		}
		seen[key] = true
		analysis, err := analyseOneDLL(plugin.DLLPath, plugin, knowledgeRoot)
		if err != nil {
			warnings = append(warnings, filepath.Base(plugin.DLLPath)+": "+err.Error())
			continue
		}
		analyses = append(analyses, analysis)
		if err := updatePluginProfileFromAnalysis(knowledgeRoot, analysis); err != nil {
			warnings = append(warnings, filepath.Base(plugin.DLLPath)+" profile: "+err.Error())
		}
	}
	// Keep Quick Analysis useful for arbitrary/unsupported native DLLs too. Any DLL
	// living beside a discovered plugin is inventoried even if RML does not know how
	// to load it yet.
	for root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".dll") {
				continue
			}
			path := filepath.Join(root, entry.Name())
			key := strings.ToLower(filepath.Clean(path))
			if seen[key] {
				continue
			}
			seen[key] = true
			analysis, err := analyseOneDLL(path, nil, knowledgeRoot)
			if err != nil {
				warnings = append(warnings, entry.Name()+": "+err.Error())
				continue
			}
			analyses = append(analyses, analysis)
			if err := updatePluginProfileFromAnalysis(knowledgeRoot, analysis); err != nil {
				warnings = append(warnings, entry.Name()+" profile: "+err.Error())
			}
		}
	}
	sort.Slice(analyses, func(i, j int) bool {
		if analyses[i].Enabled != analyses[j].Enabled {
			return analyses[i].Enabled
		}
		return strings.ToLower(analyses[i].Filename) < strings.ToLower(analyses[j].Filename)
	})
	canonicalizeAnalyses(analyses, index)
	summary := buildCompatibilitySummaryWithEvidence(analyses, index, runtimeReport)
	report := CompatibilityReport{SchemaVersion: 2, GeneratedAt: nowUTC(), Summary: summary, DLLs: analyses, Warnings: warnings}
	if err := atomicWriteJSON(filepath.Join(base, "rml", "state", "last_compatibility_report.json"), report); err != nil {
		warnings = append(warnings, "compatibility report: "+err.Error())
	}
	return summary, warnings
}

func analyseOneDLL(path string, manifestPlugin *Plugin, knowledgeRoot string) (PluginAnalysis, error) {
	var a PluginAnalysis
	a.Path = path
	a.Filename = filepath.Base(path)
	stat, err := os.Stat(path)
	if err != nil {
		return a, err
	}
	a.Size = stat.Size()
	a.SHA256, err = hashFileSHA256(path)
	if err != nil {
		return a, err
	}
	machine, is64, err := readPEMachine(path)
	if err != nil {
		return a, err
	}
	a.PE64 = is64 && machine == 0x8664
	a.Machine = fmt.Sprintf("0x%04X", machine)
	if machine == 0x8664 {
		a.Machine = "AMD64"
	}
	a.Exports, _ = peExportNames(path)
	sort.Strings(a.Exports)

	if f, err := pe.Open(path); err == nil {
		if libs, err := f.ImportedLibraries(); err == nil {
			for _, lib := range libs {
				a.Imports = appendUnique(a.Imports, strings.ToUpper(lib))
			}
		}
		if symbols, err := f.ImportedSymbols(); err == nil {
			for _, symbol := range symbols {
				if len(a.Imports) >= 512 {
					break
				}
				a.Imports = appendUnique(a.Imports, symbol)
			}
		}
		_ = f.Close()
	}
	sort.Strings(a.Imports)

	hasExport := func(name string) bool { return containsString(a.Exports, name) }
	switch {
	case hasExport("RmlPluginQuery") && hasExport("RmlPluginLoad"):
		a.LoaderType = "RML NATIVE"
	case hasExport("TsmPluginInit") || hasExport("TsmPluginApiVersion") || hasExport("TsmPluginStart"):
		a.LoaderType = "TESMIO LEGACY"
	default:
		a.LoaderType = "UNKNOWN NATIVE DLL"
	}
	if manifestPlugin != nil {
		a.PluginID = manifestPlugin.Manifest.ID
		a.WorkshopID = manifestPlugin.WorkshopID
		a.Name = manifestPlugin.Manifest.Name
		a.Version = manifestPlugin.Manifest.Version
		a.Author = manifestPlugin.Manifest.Author
		a.Enabled = manifestPlugin.Setting.Enabled
		a.Priority = manifestPlugin.Setting.Priority
		a.LoadOrder = manifestPlugin.Order
		for _, intent := range manifestPlugin.Manifest.HookIntents {
			temp := strings.ToUpper(intent.Temperature)
			if temp == "" {
				temp = symbolTemperature(intent.Symbol)
			}
			a.Interactions = append(a.Interactions, TargetInteraction{
				Symbol: intent.Symbol, Interaction: intent.Interaction, Phase: strings.ToUpper(intent.Phase),
				Evidence: "DECLARED MANIFEST", Confidence: "DECLARED", Managed: false, Temperature: temp,
			})
			a.DeclaredTargets = appendUnique(a.DeclaredTargets, intent.Symbol)
		}
		for _, symbol := range append(append([]string{}, manifestPlugin.Manifest.RequiredSymbols...), manifestPlugin.Manifest.OptionalSymbols...) {
			if !containsString(a.DeclaredTargets, symbol) {
				a.DeclaredTargets = append(a.DeclaredTargets, symbol)
				a.Interactions = append(a.Interactions, TargetInteraction{Symbol: symbol, Interaction: "symbol dependency", Evidence: "DECLARED MANIFEST", Confidence: "DECLARED", Managed: false, Temperature: symbolTemperature(symbol)})
			}
		}
	} else {
		a.Name = a.Filename
	}

	raw, _ := os.ReadFile(path)
	for _, signal := range []struct{ Needle, Label string }{
		{"MH_CreateHook", "MinHook API fingerprint"},
		{"MH_Initialize", "MinHook API fingerprint"},
		{"DetourAttach", "Microsoft Detours fingerprint"},
		{"PolyHook", "PolyHook fingerprint"},
		{"SafetyHook", "SafetyHook fingerprint"},
		{"installInlineHook", "inline-hook helper string"},
		{"patchIat", "IAT-patch helper string"},
	} {
		if bytes.Contains(raw, []byte(signal.Needle)) {
			a.HookingSignals = appendUnique(a.HookingSignals, signal.Label)
		}
	}
	for _, imported := range a.Imports {
		lower := strings.ToLower(imported)
		switch {
		case strings.Contains(lower, "virtualprotect"):
			a.HookingSignals = appendUnique(a.HookingSignals, "imports VirtualProtect")
		case strings.Contains(lower, "ntprotectvirtualmemory"):
			a.HookingSignals = appendUnique(a.HookingSignals, "imports NtProtectVirtualMemory")
		case strings.Contains(lower, "writeprocessmemory"):
			a.HookingSignals = appendUnique(a.HookingSignals, "imports WriteProcessMemory")
		case strings.Contains(lower, "createremotethread"):
			a.HookingSignals = appendUnique(a.HookingSignals, "imports CreateRemoteThread")
		case strings.Contains(lower, "virtualalloc"):
			a.HookingSignals = appendUnique(a.HookingSignals, "imports executable-memory allocator candidate")
		case strings.Contains(lower, "flushinstructioncache"):
			a.HookingSignals = appendUnique(a.HookingSignals, "imports FlushInstructionCache")
		}
	}

	if profile, err := loadPluginProfile(knowledgeRoot, a.SHA256); err == nil {
		a.ProfileState = fmt.Sprintf("KNOWN • %d observations", len(profile.Observations))
		for _, obs := range profile.Observations {
			if obs.Target == "" {
				continue
			}
			if !containsString(a.DeclaredTargets, obs.Target) {
				a.DeclaredTargets = append(a.DeclaredTargets, obs.Target)
			}
			a.Interactions = appendUniqueInteraction(a.Interactions, TargetInteraction{
				Symbol: obs.Target, Interaction: obs.Interaction, Evidence: obs.Evidence,
				Confidence: obs.Confidence, Managed: strings.HasPrefix(obs.Evidence, "rml-managed"), Temperature: symbolTemperature(obs.Target),
			})
		}
	} else {
		a.ProfileState = "NEW BINARY PROFILE"
	}

	a.Footprint = classifyFootprint(a)
	if !a.PE64 {
		a.Compatibility, a.CompatibilityWhy = "INCOMPATIBLE", "Not a conventional AMD64 PE32+ DLL."
	} else if a.LoaderType == "RML NATIVE" {
		a.Compatibility, a.CompatibilityWhy = "RML NATIVE", "RML API 1 lifecycle detected; declared manifest metadata can contribute targets to pre-launch compatibility analysis."
	} else if a.LoaderType == "TESMIO LEGACY" {
		a.Compatibility, a.CompatibilityWhy = "LEGACY HOST READY", "Tesmio API 3/4 lifecycle detected; legacy hook requests are provenance-tracked and mediated by the compatibility host."
	} else if len(a.HookingSignals) > 0 {
		a.Compatibility, a.CompatibilityWhy = "POTENTIAL CONFLICT", "Native executable-modification capability is statically visible, but targets are not yet proven."
	} else {
		a.Compatibility, a.CompatibilityWhy = "UNKNOWN", "No supported manifest or known loader lifecycle identifies its runtime behaviour."
	}
	return a, nil
}

func appendUniqueInteraction(items []TargetInteraction, value TargetInteraction) []TargetInteraction {
	for _, item := range items {
		if item.Symbol == value.Symbol && item.Interaction == value.Interaction && item.Phase == value.Phase && item.Evidence == value.Evidence {
			return items
		}
	}
	return append(items, value)
}

func symbolTemperature(symbol string) string {
	if def := semanticDefinitionByID(symbol); def != nil && def.Temperature != "" {
		return def.Temperature
	}
	if strings.Contains(symbol, "render") || strings.Contains(symbol, "tick") {
		return "HOT"
	}
	return "UNKNOWN"
}

func canonicalizeAnalyses(analyses []PluginAnalysis, index *SymbolCache) {
	for ai := range analyses {
		for ii := range analyses[ai].Interactions {
			analyses[ai].Interactions[ii].Symbol = canonicalTarget(index, analyses[ai].Interactions[ii].Symbol)
			analyses[ai].Interactions[ii].Temperature = symbolTemperature(analyses[ai].Interactions[ii].Symbol)
		}
		for i := range analyses[ai].DeclaredTargets {
			analyses[ai].DeclaredTargets[i] = canonicalTarget(index, analyses[ai].DeclaredTargets[i])
		}
		analyses[ai].DeclaredTargets = uniqueStringsSorted(analyses[ai].DeclaredTargets)
	}
}

func uniqueStringsSorted(in []string) []string {
	sort.Strings(in)
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	last := ""
	for _, v := range in {
		if v != "" && v != last {
			out = append(out, v)
			last = v
		}
	}
	return out
}

func classifyFootprint(a PluginAnalysis) string {
	if !a.PE64 {
		return "INCOMPATIBLE"
	}
	extreme := false
	for _, signal := range a.HookingSignals {
		lower := strings.ToLower(signal)
		if strings.Contains(lower, "writeprocessmemory") || strings.Contains(lower, "createremotethread") || strings.Contains(lower, "ntprotect") {
			extreme = true
		}
	}
	if extreme {
		return "EXTREME"
	}
	if a.LoaderType == "UNKNOWN NATIVE DLL" && len(a.HookingSignals) >= 2 {
		return "HIGH"
	}
	for _, target := range a.Interactions {
		if target.Temperature == "EXTREME" {
			return "HIGH"
		}
	}
	if len(a.HookingSignals) > 0 || len(a.Interactions) > 2 {
		return "MODERATE"
	}
	return "LOW"
}

func buildCompatibilitySummaryWithEvidence(analyses []PluginAnalysis, index *SymbolCache, report *RuntimeReport) CompatibilitySummary {
	var summary CompatibilitySummary
	summary.DLLCount = len(analyses)
	targets := map[string]*CompatibilityTarget{}
	currentHashes := map[string]string{}
	currentEnabled := map[string]bool{}
	currentPriorities := map[string]int{}
	currentOrders := map[string]int{}
	for _, a := range analyses {
		switch a.LoaderType {
		case "RML NATIVE":
			summary.RMLNative++
		case "TESMIO LEGACY":
			summary.TesmioLegacy++
		default:
			summary.UnknownNative++
		}
		if a.ProfileState == "NEW BINARY PROFILE" {
			summary.UnknownProfiles++
		}
		if a.PluginID != "" {
			currentHashes[a.PluginID] = strings.ToLower(a.SHA256)
			currentEnabled[a.PluginID] = a.Enabled
			currentPriorities[a.PluginID] = a.Priority
			currentOrders[a.PluginID] = a.LoadOrder
		}
		if !a.Enabled {
			continue
		}
		summary.SelectedDLLs++
		for _, interaction := range a.Interactions {
			if interaction.Symbol == "" {
				continue
			}
			symbol := canonicalTarget(index, interaction.Symbol)
			t := targets[symbol]
			if t == nil {
				t = &CompatibilityTarget{Symbol: symbol, Temperature: symbolTemperature(symbol)}
				if def := semanticDefinitionByID(symbol); def != nil {
					t.DisplayName = def.DisplayName
				}
				targets[symbol] = t
			}
			t.Interactions = append(t.Interactions, CompatibilityTargetInteraction{PluginID: a.PluginID, PluginName: a.Name, LoaderType: a.LoaderType, Action: interaction.Interaction, Phase: interaction.Phase, Evidence: interaction.Evidence, Managed: interaction.Managed})
		}
	}

	// Lifecycle failure and unmanaged-write evidence are separate from target sharing.
	if report != nil && index != nil && strings.EqualFold(report.Game.SHA256, index.Game.SHA256) {
		for _, rp := range report.Plugins {
			if rp.ID == "" || !currentEnabled[rp.ID] || !strings.EqualFold(currentHashes[rp.ID], rp.SHA256) ||
				currentPriorities[rp.ID] != rp.Priority || currentOrders[rp.ID] != rp.LoadOrder {
				continue
			}
			if rp.Failed {
				summary.LifecycleFailures++
			}
			summary.UnmanagedChanges += rp.UnmanagedInitChanges + rp.UnmanagedStartChanges
		}
	}

	chains := compatibleRuntimeChains(index, report, currentHashes, currentEnabled, currentPriorities, currentOrders)
	for _, t := range targets {
		summary.KnownTargets++
		ownerManaged := map[string]bool{}
		ownerSeen := map[string]bool{}
		for _, i := range t.Interactions {
			key := strings.TrimSpace(i.PluginID)
			if key == "" {
				key = i.LoaderType + "|" + i.PluginName
			}
			ownerSeen[key] = true
			if i.Managed {
				ownerManaged[key] = true
			}
		}
		managedOwners := 0
		for key := range ownerSeen {
			if ownerManaged[key] {
				managedOwners++
			}
		}
		if len(ownerSeen) <= 1 {
			t.Result, t.Reason = "SINGLE OWNER", "Only one selected DLL is currently known to interact with this target; duplicate evidence rows from that DLL are not a conflict."
			summary.Targets = append(summary.Targets, *t)
			continue
		}
		summary.SharedTargets++
		chain, haveChain := chains[t.Symbol]
		if haveChain && !chainCoversOwners(chain.Steps, chain.ObservedTopOwner, ownerSeen) {
			haveChain = false
		}
		if haveChain {
			t.Proof = chain.Proof
			for _, step := range chain.Steps {
				t.Chain = append(t.Chain, CompatibilityChainStep{PluginID: step.PluginID, PluginSHA256: step.PluginSHA256, Label: step.Label, ExecutionOrder: step.ExecutionOrder})
			}
			switch chain.Integrity {
			case "PASS":
				t.Result = "SHARED — PROVEN HEALTHY"
				t.Reason = "The last matching runtime session observed this exact game/plugin build combination and the recorded execution chain passed integrity validation."
				summary.ProvenHealthyShared++
				if len(chain.Steps) > 1 {
					summary.OrderSensitive++
				}
			case "RECOGNIZED EXTERNAL CHAIN":
				t.Result = "SHARED — RECOGNIZED EXTERNAL CHAIN"
				t.Reason = "RML observed a later executable detour outside its direct host chain and could identify/preserve the effective executable path. This is compatible evidence, but not fully host-owned proof."
				summary.RecognizedExternal++
				summary.OrderSensitive++
			default:
				t.Result = "ACTUAL CONFLICT"
				t.Reason = "The matching runtime chain did not validate: the target reverted, became invalid, or contains an unrecognized modification."
				summary.PotentialConflicts++
			}
		} else if managedOwners == len(ownerSeen) {
			t.Result, t.Reason = "SHARED — DECLARED MANAGED", "Multiple owners are backed by RML-managed runtime evidence, but no matching current-session chain proof is available."
			summary.ManagedShared++
		} else {
			t.Result, t.Reason = "SHARED — UNPROVEN", "Multiple DLL owners are known for this target, but no matching runtime chain has been verified for the current executable and DLL hashes. Run Detailed Diagnostic Logging to capture the combination."
			summary.UnprovenShared++
			if managedOwners > 0 && managedOwners < len(ownerSeen) {
				summary.BridgeCandidates++
			}
		}
		summary.Targets = append(summary.Targets, *t)
	}
	sort.Slice(summary.Targets, func(i, j int) bool { return summary.Targets[i].Symbol < summary.Targets[j].Symbol })
	switch {
	case summary.SelectedDLLs == 0:
		summary.Overall, summary.OverallClass = "NO DLLS ENABLED", 0
	case summary.LifecycleFailures > 0:
		summary.Overall, summary.OverallClass = "PLUGIN START FAILURE", 2
	case summary.PotentialConflicts > 0:
		summary.Overall, summary.OverallClass = "ACTUAL CONFLICT", 2
	case summary.UnprovenShared > 0:
		summary.Overall, summary.OverallClass = "SHARED TARGETS REQUIRE PROOF", 1
	case summary.RecognizedExternal > 0:
		summary.Overall, summary.OverallClass = "RECOGNIZED EXTERNAL CHAINS", 1
	case summary.ProvenHealthyShared > 0 || summary.ManagedShared > 0:
		summary.Overall, summary.OverallClass = "SHARED CHAINS HEALTHY", 0
	default:
		summary.Overall, summary.OverallClass = "NO KNOWN OVERLAPS", 0
	}
	return summary
}

func compatibleRuntimeChains(index *SymbolCache, report *RuntimeReport, currentHashes map[string]string, currentEnabled map[string]bool, currentPriorities map[string]int, currentOrders map[string]int) map[string]struct {
	Integrity        string
	Proof            string
	ObservedTopOwner string
	Steps            []struct {
		PluginID, PluginSHA256, Label string
		ExecutionOrder                int
	}
} {
	out := map[string]struct {
		Integrity        string
		Proof            string
		ObservedTopOwner string
		Steps            []struct {
			PluginID, PluginSHA256, Label string
			ExecutionOrder                int
		}
	}{}
	if index == nil || report == nil || !strings.EqualFold(index.Game.SHA256, report.Game.SHA256) {
		return out
	}
	reportPlugins := map[string]struct {
		SHA256    string
		Priority  int
		LoadOrder int
	}{}
	for _, rp := range report.Plugins {
		if rp.ID != "" {
			reportPlugins[rp.ID] = struct {
				SHA256    string
				Priority  int
				LoadOrder int
			}{strings.ToLower(rp.SHA256), rp.Priority, rp.LoadOrder}
		}
	}
	ownerEnvironmentMatches := func(id, sha string) bool {
		if id == "" || !currentEnabled[id] || !strings.EqualFold(currentHashes[id], sha) {
			return false
		}
		rp, ok := reportPlugins[id]
		return ok && strings.EqualFold(rp.SHA256, currentHashes[id]) && rp.Priority == currentPriorities[id] && rp.LoadOrder == currentOrders[id]
	}
	for _, c := range report.HookChains {
		symbol := canonicalTarget(index, c.SymbolID)
		if symbol == "" {
			symbol = canonicalTarget(index, c.Target)
		}
		if symbol == "" {
			continue
		}
		valid := true
		steps := make([]struct {
			PluginID, PluginSHA256, Label string
			ExecutionOrder                int
		}, 0, len(c.Steps))
		for _, st := range c.Steps {
			if !ownerEnvironmentMatches(st.PluginID, st.PluginSHA256) {
				valid = false
				break
			}
			steps = append(steps, struct {
				PluginID, PluginSHA256, Label string
				ExecutionOrder                int
			}{st.PluginID, st.PluginSHA256, st.Label, st.ExecutionOrder})
		}
		if !valid || len(steps) == 0 {
			continue
		}
		if strings.EqualFold(c.Integrity, "RECOGNIZED EXTERNAL CHAIN") {
			// A decodable jump shape is useful evidence, but it is not ownership proof
			// unless the observed top destination belongs to a current matching plugin.
			if c.ObservedTopOwner == "" {
				continue
			}
			rp, ok := reportPlugins[c.ObservedTopOwner]
			if !ok || !ownerEnvironmentMatches(c.ObservedTopOwner, rp.SHA256) {
				continue
			}
		}
		out[symbol] = struct {
			Integrity        string
			Proof            string
			ObservedTopOwner string
			Steps            []struct {
				PluginID, PluginSHA256, Label string
				ExecutionOrder                int
			}
		}{c.Integrity, c.Proof, c.ObservedTopOwner, steps}
	}
	return out
}

func chainCoversOwners(steps []struct {
	PluginID, PluginSHA256, Label string
	ExecutionOrder                int
}, observedTopOwner string, owners map[string]bool) bool {
	covered := map[string]bool{}
	for _, step := range steps {
		if step.PluginID != "" {
			covered[step.PluginID] = true
		}
	}
	if observedTopOwner != "" {
		covered[observedTopOwner] = true
	}
	for owner := range owners {
		if owner == "" || strings.Contains(owner, "|") {
			// Anonymous/non-plugin owners cannot be proven by a structured runtime chain.
			return false
		}
		if !covered[owner] {
			return false
		}
	}
	return true
}
