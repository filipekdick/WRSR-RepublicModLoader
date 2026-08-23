package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

const (
	planAllowProbable      = 1 << 0
	planUseSymbolCache     = 1 << 1
	planFilterWorkshop     = 1 << 2
	planDeepTest           = 1 << 3
	pluginForced           = 1 << 0
	pluginTesmioLegacy     = 1 << 1
	pluginRawNative        = 1 << 2
	pluginManifestIdentity = 1 << 3
)

func putU32(buf *bytes.Buffer, value uint32) {
	var x [4]byte
	binary.LittleEndian.PutUint32(x[:], value)
	buf.Write(x[:])
}

func putU64(buf *bytes.Buffer, value uint64) {
	var x [8]byte
	binary.LittleEndian.PutUint64(x[:], value)
	buf.Write(x[:])
}

func fixedASCII(value string, n int) []byte {
	out := make([]byte, n)
	if len(value) >= n {
		value = value[:n-1]
	}
	copy(out, value)
	return out
}

func profileCode(id LaunchProfile) uint32 {
	switch id {
	case profileNormal:
		return 8
	case profileDeepTest:
		return 9
	default:
		return 8
	}
}

func writeRuntimePlan(path string, cfg AppConfig, game GameIdentity, ordered []*Plugin, profile LaunchProfileInfo, sessionID string, cache *SymbolCache, workshopItems []WorkshopItem) error {
	if len(ordered) > 128 {
		return fmt.Errorf("runtime plan contains more than 128 plugins")
	}
	var cached []CachedSymbol
	if cache != nil {
		for _, symbol := range cache.Symbols {
			_, semanticKnown := loaderSymbolCatalog[symbol.ID]
			observedKnown := strings.HasPrefix(symbol.ID, "wrsr.observed.rva_")
			if (!semanticKnown && !observedKnown) || symbol.RVA == 0 || symbol.EntryFingerprint == "" {
				continue
			}
			cached = append(cached, symbol)
		}
		sort.Slice(cached, func(i, j int) bool { return cached[i].ID < cached[j].ID })
	}
	if len(cached) > 256 {
		return fmt.Errorf("runtime plan contains more than 256 cached symbols")
	}

	// Workshop visibility is allow-list based: only IDs enabled in the active
	// launcher configuration are forwarded to WRSR for this session.
	enabledWorkshop := make([]uint64, 0, len(workshopItems))
	workshopInventoryValid := false
	root := workshopRootFromGamePath(game.Path)
	if root != "" {
		// An unavailable Workshop directory must not turn an empty inventory into
		// an accidental "hide every mod" allow-list.
		if _, err := os.ReadDir(root); err == nil {
			workshopInventoryValid = true
		}
	}
	if workshopInventoryValid {
		seen := map[uint64]bool{}
		for _, item := range workshopItems {
			if !item.Enabled || !validWorkshopID(item.ID) {
				continue
			}
			n, err := strconv.ParseUint(item.ID, 10, 64)
			if err == nil && n != 0 && !seen[n] {
				seen[n] = true
				enabledWorkshop = append(enabledWorkshop, n)
			}
		}
		sort.Slice(enabledWorkshop, func(i, j int) bool { return enabledWorkshop[i] < enabledWorkshop[j] })
	}
	if len(enabledWorkshop) > maxWorkshopItems {
		return fmt.Errorf("runtime plan contains more than %d enabled Workshop items", maxWorkshopItems)
	}

	var buf bytes.Buffer
	buf.WriteString("RMLPLAN7")
	putU32(&buf, 7)
	flags := uint32(0)
	if cfg.AllowProbable {
		flags |= planAllowProbable
	}
	if profile.DeepTest {
		flags |= planDeepTest
	}
	if cache != nil {
		flags |= planUseSymbolCache
	}
	if workshopInventoryValid {
		flags |= planFilterWorkshop
	}
	putU32(&buf, flags)
	putU32(&buf, profileCode(profile.ID))
	putU32(&buf, uint32(len(ordered)))
	putU32(&buf, uint32(len(cached)))
	putU32(&buf, uint32(len(enabledWorkshop)))
	buf.Write(fixedASCII(game.Version, 32))
	buf.Write(fixedASCII(game.SHA256, 65))
	buf.Write(fixedASCII(sessionID, 64))

	for _, symbol := range cached {
		fingerprint, err := hex.DecodeString(symbol.EntryFingerprint)
		if err != nil || len(fingerprint) == 0 || len(fingerprint) > 64 {
			return fmt.Errorf("cached symbol %s has an invalid entry fingerprint", symbol.ID)
		}
		id := []byte(symbol.ID)
		putU32(&buf, uint32(len(id)))
		putU32(&buf, symbol.RVA)
		putU32(&buf, uint32(symbol.FunctionSize))
		confidence := uint32(1)
		if symbol.Confidence == "CONFIRMED" {
			confidence = 2
		}
		if symbol.Confidence == "OBSERVED" {
			confidence = 5
		}
		putU32(&buf, confidence)
		putU32(&buf, uint32(len(fingerprint)))
		buf.Write(id)
		buf.Write(fingerprint)
	}

	for _, plugin := range ordered {
		if !validPluginID(plugin.Manifest.ID) || len(plugin.Manifest.Version) == 0 || len(plugin.Manifest.Version) > 31 {
			return fmt.Errorf("plugin %q has an invalid runtime identity", plugin.Manifest.ID)
		}
		requiredSymbols := plugin.Manifest.RequiredSymbols
		if plugin.LoaderKind != pluginKindRML {
			// RequiredSymbols is an RML-native contract, not a generic DLL
			// admission rule. Ignore stale/optional manifest symbol metadata for
			// Tesmio and raw DLLs so those paths remain permissive.
			requiredSymbols = nil
		}
		if len(requiredSymbols) > 32 {
			return fmt.Errorf("plugin %s declares more than 32 required symbols", plugin.Manifest.ID)
		}
		pluginFlags := uint32(0)
		if plugin.Setting.Force {
			pluginFlags |= pluginForced
		}
		if plugin.LoaderKind == pluginKindTesmio {
			pluginFlags |= pluginTesmioLegacy
		} else if plugin.LoaderKind == pluginKindRaw {
			pluginFlags |= pluginRawNative
		}
		if plugin.ManifestPath != "" {
			// Only an explicit, valid sidecar manifest makes the runtime identity
			// contract strict. Manifestless RML-aware DLLs receive a synthetic
			// launcher ID for bookkeeping, but their own descriptor ID is allowed
			// to differ because manifests are intentionally optional.
			pluginFlags |= pluginManifestIdentity
		}
		id := []byte(plugin.Manifest.ID)
		version := []byte(plugin.Manifest.Version)
		hash := []byte(plugin.DLLSHA256)
		if len(hash) != 64 {
			return fmt.Errorf("plugin %s could not be fingerprinted", plugin.Manifest.ID)
		}
		path16 := utf16.Encode([]rune(plugin.DLLPath))
		if len(path16) == 0 || len(path16) > 4096 {
			return fmt.Errorf("plugin %s DLL path is outside the runtime length limit", plugin.Manifest.ID)
		}
		putU32(&buf, pluginFlags)
		putU32(&buf, uint32(int32(plugin.Setting.Priority)))
		putU32(&buf, uint32(len(id)))
		putU32(&buf, uint32(len(version)))
		putU32(&buf, uint32(len(hash)))
		putU32(&buf, uint32(len(path16)))
		putU32(&buf, uint32(len(requiredSymbols)))
		buf.Write(id)
		buf.Write(version)
		buf.Write(hash)
		for _, u := range path16 {
			var pair [2]byte
			binary.LittleEndian.PutUint16(pair[:], u)
			buf.Write(pair[:])
		}
		for _, required := range requiredSymbols {
			if !validPluginID(required) {
				return fmt.Errorf("plugin %s has invalid required symbol %q", plugin.Manifest.ID, required)
			}
			b := []byte(required)
			putU32(&buf, uint32(len(b)))
			buf.Write(b)
		}
	}
	for _, id := range enabledWorkshop {
		putU64(&buf, id)
	}
	if buf.Len() > 16*1024*1024 {
		return fmt.Errorf("runtime plan exceeds 16 MiB")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
