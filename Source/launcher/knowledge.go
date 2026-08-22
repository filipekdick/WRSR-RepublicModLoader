package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	symbolCacheSchemaVersion   = 3
	pluginProfileSchemaVersion = 1
	resolverEngineVersion      = 15
)

type CachedSymbol struct {
	ID               string   `json:"id"`
	DisplayName      string   `json:"displayName,omitempty"`
	Semantic         bool     `json:"semantic,omitempty"`
	Aliases          []string `json:"aliases,omitempty"`
	RVA              uint32   `json:"rva"`
	FunctionSize     int      `json:"functionSize"`
	Confidence       string   `json:"confidence"`
	Resolver         string   `json:"resolver"`
	EntryFingerprint string   `json:"entryFingerprint"`
	Temperature      string   `json:"temperature"`
}

type SymbolCache struct {
	SchemaVersion  int    `json:"schemaVersion"`
	ResolverEngine int    `json:"resolverEngineVersion"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
	Game           struct {
		Version string `json:"version"`
		SHA256  string `json:"sha256"`
		Size    int64  `json:"size"`
	} `json:"game"`
	Symbols map[string]CachedSymbol `json:"symbols"`
}

type BehaviorObservation struct {
	Event            string   `json:"event"`
	Target           string   `json:"target,omitempty"`
	Interaction      string   `json:"interaction,omitempty"`
	Evidence         string   `json:"evidence"`
	Confidence       string   `json:"confidence"`
	FirstSeen        string   `json:"firstSeen"`
	LastSeen         string   `json:"lastSeen"`
	SessionsObserved int      `json:"sessionsObserved"`
	GameVersions     []string `json:"gameVersions,omitempty"`
	SessionIDs       []string `json:"sessionIds,omitempty"`
	Details          []string `json:"details,omitempty"`
}

type PluginBehaviorProfile struct {
	SchemaVersion int      `json:"schemaVersion"`
	SHA256        string   `json:"sha256"`
	FirstSeen     string   `json:"firstSeen"`
	LastSeen      string   `json:"lastSeen"`
	Filenames     []string `json:"filenames"`
	Identity      struct {
		PluginID   string `json:"pluginId,omitempty"`
		Name       string `json:"name,omitempty"`
		Version    string `json:"version,omitempty"`
		Author     string `json:"author,omitempty"`
		LoaderType string `json:"loaderType"`
	} `json:"identity"`
	Binary struct {
		Size    int64  `json:"size"`
		Machine string `json:"machine"`
		PE64    bool   `json:"pe64"`
	} `json:"binary"`
	StaticAnalysis struct {
		Imports               []string `json:"imports,omitempty"`
		Exports               []string `json:"exports,omitempty"`
		HookingSignals        []string `json:"hookingSignals,omitempty"`
		DeclaredTargets       []string `json:"declaredTargets,omitempty"`
		ModificationFootprint string   `json:"modificationFootprint"`
	} `json:"staticAnalysis"`
	Observations       []BehaviorObservation `json:"observations"`
	LastSessionOutcome string                `json:"lastSessionOutcome,omitempty"`
}

type PluginFamilyVersion struct {
	SHA256    string `json:"sha256"`
	Version   string `json:"version,omitempty"`
	FirstSeen string `json:"firstSeen"`
	LastSeen  string `json:"lastSeen"`
}

type PluginFamilyProfile struct {
	SchemaVersion int                   `json:"schemaVersion"`
	PluginID      string                `json:"pluginId"`
	Name          string                `json:"name,omitempty"`
	Versions      []PluginFamilyVersion `json:"versions"`
}

type JournalEvent struct {
	SchemaVersion int    `json:"schemaVersion"`
	Timestamp     string `json:"timestamp"`
	SessionID     string `json:"sessionId"`
	GameVersion   string `json:"gameVersion,omitempty"`
	DLLSHA256     string `json:"dllSha256,omitempty"`
	PluginID      string `json:"pluginId,omitempty"`
	PluginName    string `json:"pluginName,omitempty"`
	PluginVersion string `json:"pluginVersion,omitempty"`
	Event         string `json:"event"`
	Target        string `json:"target,omitempty"`
	Interaction   string `json:"interaction,omitempty"`
	Evidence      string `json:"evidence,omitempty"`
	Detail        string `json:"detail,omitempty"`
	ExitCode      uint32 `json:"exitCode,omitempty"`
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func hashFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func atomicWriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rml-knowledge-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}

func persistentKnowledgeRoot(base string) string {
	if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
		return filepath.Join(local, "RepublicModLoader", "knowledge")
	}
	// Portable/fallback mode for unusual Windows environments.
	return filepath.Join(base, "rml", "knowledge")
}

func symbolCachePath(base string, game *GameIdentity) string {
	if game == nil || game.SHA256 == "" {
		return ""
	}
	return filepath.Join(base, "games", game.SHA256, "resolver-cache.json")
}

// loadSymbolCache restores only exact-build address knowledge. Known WRSR builds
// are seeded from the semantic catalogue; unknown builds resolve at runtime and
// populate the cache from validated runtime observations.
func loadSymbolCache(base string, game *GameIdentity) (*SymbolCache, string, error) {
	path := symbolCachePath(base, game)
	if path == "" {
		return nil, "UNAVAILABLE", errors.New("game fingerprint unavailable")
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		now := nowUTC()
		cache := &SymbolCache{SchemaVersion: symbolCacheSchemaVersion, ResolverEngine: resolverEngineVersion, CreatedAt: now, UpdatedAt: now, Symbols: map[string]CachedSymbol{}}
		cache.Game.Version, cache.Game.SHA256, cache.Game.Size = game.Version, game.SHA256, game.Size
		if changed, buildErr := promoteKnownSemanticSymbols(game, cache); buildErr == nil && changed {
			_ = atomicWriteJSON(path, cache)
			return cache, fmt.Sprintf("READY (%d symbols)", len(cache.Symbols)), nil
		}
		return cache, "EMPTY", nil
	}
	if err != nil {
		return nil, "ERROR", err
	}
	var cache SymbolCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, "INVALID", err
	}
	if cache.Game.SHA256 != game.SHA256 {
		return nil, "INVALID", errors.New("cache fingerprint mismatch")
	}
	if cache.Symbols == nil {
		cache.Symbols = map[string]CachedSymbol{}
	}
	cache.SchemaVersion = symbolCacheSchemaVersion
	cache.ResolverEngine = resolverEngineVersion
	if changed, promoteErr := promoteKnownSemanticSymbols(game, &cache); promoteErr == nil && changed {
		cache.UpdatedAt = nowUTC()
		_ = atomicWriteJSON(path, &cache)
	}
	return &cache, fmt.Sprintf("READY (%d symbols)", len(cache.Symbols)), nil
}

type runtimeFunctionCandidate struct{ Begin, End uint32 }

func runtimeFunctionCandidates(layout *peLayout) []runtimeFunctionCandidate {
	if layout == nil || !layout.Is64 {
		return nil
	}
	rva, size := layout.Directories[3][0], layout.Directories[3][1]
	if rva == 0 || size < 12 {
		return nil
	}
	off, ok := layout.rvaToOffset(rva)
	if !ok {
		return nil
	}
	count := int(size / 12)
	if count > 200000 {
		return nil
	}
	out := make([]runtimeFunctionCandidate, 0, count)
	for i := 0; i < count; i++ {
		p := off + i*12
		if p+12 > len(layout.Data) {
			break
		}
		b := uint32(layout.Data[p]) | uint32(layout.Data[p+1])<<8 | uint32(layout.Data[p+2])<<16 | uint32(layout.Data[p+3])<<24
		e := uint32(layout.Data[p+4]) | uint32(layout.Data[p+5])<<8 | uint32(layout.Data[p+6])<<16 | uint32(layout.Data[p+7])<<24
		if b == 0 || e <= b {
			continue
		}
		if _, ok := layout.rvaToOffset(b); !ok {
			continue
		}
		out = append(out, runtimeFunctionCandidate{Begin: b, End: e})
	}
	return out
}

func uniqueStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	sort.Strings(in)
	out := in[:0]
	last := ""
	for _, v := range in {
		if v == "" || v == last {
			continue
		}
		out = append(out, v)
		last = v
	}
	return out
}

func mergeRuntimeReportIntoSymbolCache(base string, game *GameIdentity, report *RuntimeReport) error {
	if game == nil || report == nil || game.SHA256 == "" || report.Game.SHA256 != game.SHA256 {
		return nil
	}
	cache, _, err := loadSymbolCache(base, game)
	if err != nil || cache == nil {
		now := nowUTC()
		cache = &SymbolCache{SchemaVersion: symbolCacheSchemaVersion, ResolverEngine: resolverEngineVersion, CreatedAt: now, UpdatedAt: now, Symbols: map[string]CachedSymbol{}}
		cache.Game.Version, cache.Game.SHA256, cache.Game.Size = game.Version, game.SHA256, game.Size
	}
	changed := false
	for _, symbol := range report.Symbols {
		if symbol.ID == "" || symbol.RVA == 0 || symbol.EntryFingerprint == "" {
			continue
		}
		if symbol.Confidence != "CONFIRMED" && symbol.Confidence != "OBSERVED" {
			continue
		}
		existing := cache.Symbols[symbol.ID]
		record := existing
		record.ID = symbol.ID
		record.RVA = symbol.RVA
		record.FunctionSize = symbol.FunctionSize
		record.Confidence = symbol.Confidence
		record.Resolver = symbol.Resolver
		record.EntryFingerprint = strings.ToUpper(symbol.EntryFingerprint)
		record.Temperature = symbol.Temperature
		if record.Temperature == "" {
			record.Temperature = "UNKNOWN"
		}
		cache.Symbols[symbol.ID] = record
		changed = true
	}
	if !changed {
		return nil
	}
	cache.SchemaVersion = symbolCacheSchemaVersion
	cache.ResolverEngine = resolverEngineVersion
	cache.Game.Version, cache.Game.SHA256, cache.Game.Size = game.Version, game.SHA256, game.Size
	cache.UpdatedAt = nowUTC()
	return atomicWriteJSON(symbolCachePath(base, game), cache)
}

func pluginProfilePath(base, hash string) string {
	return filepath.Join(base, "plugins", "binaries", strings.ToLower(hash)+".rmlprofile.json")
}

func loadPluginProfile(base, hash string) (*PluginBehaviorProfile, error) {
	if len(hash) != 64 {
		return nil, errors.New("invalid DLL hash")
	}
	data, err := os.ReadFile(pluginProfilePath(base, hash))
	if err != nil {
		return nil, err
	}
	var profile PluginBehaviorProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, err
	}
	return &profile, nil
}

func updatePluginProfileFromAnalysis(base string, analysis PluginAnalysis) error {
	if len(analysis.SHA256) != 64 {
		return nil
	}
	profile, err := loadPluginProfile(base, analysis.SHA256)
	if err != nil {
		profile = &PluginBehaviorProfile{SchemaVersion: pluginProfileSchemaVersion, SHA256: analysis.SHA256, FirstSeen: nowUTC()}
	}
	profile.SchemaVersion = pluginProfileSchemaVersion
	profile.SHA256 = analysis.SHA256
	profile.LastSeen = nowUTC()
	profile.Filenames = appendUnique(profile.Filenames, analysis.Filename)
	profile.Identity.PluginID = analysis.PluginID
	profile.Identity.Name = analysis.Name
	profile.Identity.Version = analysis.Version
	profile.Identity.Author = analysis.Author
	profile.Identity.LoaderType = analysis.LoaderType
	profile.Binary.Size = analysis.Size
	profile.Binary.Machine = analysis.Machine
	profile.Binary.PE64 = analysis.PE64
	profile.StaticAnalysis.Imports = append([]string(nil), analysis.Imports...)
	profile.StaticAnalysis.Exports = append([]string(nil), analysis.Exports...)
	profile.StaticAnalysis.HookingSignals = append([]string(nil), analysis.HookingSignals...)
	profile.StaticAnalysis.DeclaredTargets = append([]string(nil), analysis.DeclaredTargets...)
	profile.StaticAnalysis.ModificationFootprint = analysis.Footprint
	if err := atomicWriteJSON(pluginProfilePath(base, analysis.SHA256), profile); err != nil {
		return err
	}
	if analysis.PluginID != "" {
		return updatePluginFamily(base, analysis, profile)
	}
	return nil
}

func updatePluginFamily(base string, analysis PluginAnalysis, profile *PluginBehaviorProfile) error {
	familyPath := filepath.Join(base, "plugins", "families", analysis.PluginID+".json")
	family := PluginFamilyProfile{SchemaVersion: 1, PluginID: analysis.PluginID, Name: analysis.Name}
	if data, err := os.ReadFile(familyPath); err == nil {
		_ = json.Unmarshal(data, &family)
	}
	found := false
	for i := range family.Versions {
		if family.Versions[i].SHA256 == analysis.SHA256 {
			family.Versions[i].Version = analysis.Version
			family.Versions[i].LastSeen = profile.LastSeen
			found = true
		}
	}
	if !found {
		family.Versions = append(family.Versions, PluginFamilyVersion{SHA256: analysis.SHA256, Version: analysis.Version, FirstSeen: profile.FirstSeen, LastSeen: profile.LastSeen})
	}
	sort.Slice(family.Versions, func(i, j int) bool { return family.Versions[i].FirstSeen < family.Versions[j].FirstSeen })
	return atomicWriteJSON(familyPath, family)
}

func appendUnique(items []string, value string) []string {
	if value == "" {
		return items
	}
	for _, item := range items {
		if strings.EqualFold(item, value) {
			return items
		}
	}
	return append(items, value)
}

func mergeSessionJournals(runtimeBase, knowledgeRoot string) (int, []string) {
	dir := filepath.Join(runtimeBase, "rml", "state", "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, []string{err.Error()}
	}
	merged := 0
	var warnings []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".journal") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		lockPath := strings.TrimSuffix(path, filepath.Ext(path)) + ".lock"
		if _, statErr := os.Stat(lockPath); statErr == nil {
			// The runtime holds this file with share mode zero for the full WRSR
			// session. Opening it succeeds only after that process has exited (or
			// when a stale lock remains after a crash). Never merge an active
			// journal merely because no new event has been written recently.
			lock, lockErr := os.OpenFile(lockPath, os.O_RDWR, 0)
			if lockErr != nil {
				continue
			}
			_ = lock.Close()
			_ = os.Remove(lockPath)
		}
		if err := mergeOneJournal(knowledgeRoot, path); err != nil {
			warnings = append(warnings, entry.Name()+": "+err.Error())
			continue
		}
		if err := os.Remove(path); err != nil {
			warnings = append(warnings, entry.Name()+": merged but could not remove journal: "+err.Error())
			continue
		}
		merged++
	}
	return merged, warnings
}

func mergeOneJournal(knowledgeRoot, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var events []JournalEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event JournalEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return err
		}
		if event.SchemaVersion != 1 || event.Event == "" || event.SessionID == "" {
			return errors.New("invalid journal event")
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(events) == 0 {
		return errors.New("empty journal")
	}
	outcome := "ABNORMAL OR LAUNCHER CLOSED"
	for _, event := range events {
		if event.Event == "session_end" {
			if event.ExitCode == 0 {
				outcome = "NORMAL EXIT"
			} else {
				outcome = fmt.Sprintf("EXIT CODE 0x%X", event.ExitCode)
			}
		}
	}
	profiles := map[string]*PluginBehaviorProfile{}
	for _, event := range events {
		if len(event.DLLSHA256) != 64 {
			continue
		}
		profile := profiles[event.DLLSHA256]
		if profile == nil {
			profile, _ = loadPluginProfile(knowledgeRoot, event.DLLSHA256)
			if profile == nil {
				profile = &PluginBehaviorProfile{SchemaVersion: 1, SHA256: event.DLLSHA256, FirstSeen: event.Timestamp}
			}
			profiles[event.DLLSHA256] = profile
		}
		profile.LastSeen = event.Timestamp
		profile.LastSessionOutcome = outcome
		if profile.Identity.PluginID == "" {
			profile.Identity.PluginID = event.PluginID
			profile.Identity.Name = event.PluginName
			profile.Identity.Version = event.PluginVersion
		}
		mergeBehaviorObservation(profile, event)
	}
	for hash, profile := range profiles {
		if err := atomicWriteJSON(pluginProfilePath(knowledgeRoot, hash), profile); err != nil {
			return err
		}
	}
	return nil
}

func mergeBehaviorObservation(profile *PluginBehaviorProfile, event JournalEvent) {
	if event.Event == "session_start" || event.Event == "session_end" {
		return
	}
	key := event.Event + "|" + event.Target + "|" + event.Interaction
	for i := range profile.Observations {
		o := &profile.Observations[i]
		if o.Event+"|"+o.Target+"|"+o.Interaction != key {
			continue
		}
		o.LastSeen = event.Timestamp
		o.GameVersions = appendUnique(o.GameVersions, event.GameVersion)
		if !containsString(o.SessionIDs, event.SessionID) {
			o.SessionIDs = append(o.SessionIDs, event.SessionID)
			o.SessionsObserved++
		}
		o.Details = appendUnique(o.Details, event.Detail)
		return
	}
	confidence := "OBSERVED CURRENT"
	if event.Evidence == "rml-managed" {
		confidence = "CONFIRMED CURRENT"
	}
	profile.Observations = append(profile.Observations, BehaviorObservation{
		Event: event.Event, Target: event.Target, Interaction: event.Interaction,
		Evidence: event.Evidence, Confidence: confidence,
		FirstSeen: event.Timestamp, LastSeen: event.Timestamp,
		SessionsObserved: 1, GameVersions: nonEmptySlice(event.GameVersion),
		SessionIDs: []string{event.SessionID}, Details: nonEmptySlice(event.Detail),
	})
}

func nonEmptySlice(value string) []string {
	if value == "" {
		return nil
	}
	return []string{value}
}

func newSessionID() string {
	return fmt.Sprintf("%s-%d", time.Now().UTC().Format("20060102T150405.000000000Z"), os.Getpid())
}
