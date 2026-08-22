//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const wrsrSteamAppID = "784150"

type WorkshopItem struct {
	ID                      string
	Name                    string
	Description             string
	Folder                  string
	Thumbnail               string
	ConfigPath              string
	Enabled                 bool
	Unsubscribed            bool
	Unsupported             bool
	UnsupportedReasons      []string
	LocalUnsupportedReasons []string
	RemoteSupportState      string
	RemoteSupportReason     string
	RemoteCheckedUnix       int64
	PluginDirs              []string // top-level or one-level-deep plugin/plugins folders supplied by this Workshop item
	EmbeddedPluginIDs       []string // populated after DLL discovery for launcher dependency coupling
	UpdateState             string
	UpdateReason            string
	UpdateCheckedUnix       int64
	InstalledUpdatedUnix    int64
	RemoteUpdatedUnix       int64
	InstalledManifest       string
	LatestManifest          string
	SteamItemState          uint32
}

var workshopBBCode = regexp.MustCompile(`\[[^\]]+\]`)

func steamappsFromGamePath(gamePath string) string {
	if gamePath == "" {
		return ""
	}
	// .../steamapps/common/SovietRepublic/SOVIET64.exe
	gameDir := filepath.Dir(gamePath)
	common := filepath.Dir(gameDir)
	if !strings.EqualFold(filepath.Base(common), "common") {
		return ""
	}
	steamapps := filepath.Dir(common)
	if !strings.EqualFold(filepath.Base(steamapps), "steamapps") {
		return ""
	}
	return steamapps
}

func workshopRootFromGamePath(gamePath string) string {
	steamapps := steamappsFromGamePath(gamePath)
	if steamapps == "" {
		return ""
	}
	return filepath.Join(steamapps, "workshop", "content", wrsrSteamAppID)
}

func validWorkshopID(id string) bool {
	if len(id) == 0 || len(id) > 32 {
		return false
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func findWorkshopPluginDirs(root string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(path string) {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			return
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = filepath.Clean(path)
		}
		key := strings.ToLower(filepath.Clean(abs))
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, abs)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		if name == "plugin" || name == "plugins" {
			add(filepath.Join(root, e.Name()))
		}
	}
	// A number of WRSR items wrap their actual mod payload in one project/item
	// directory. Supporting one extra level catches `<item>/<payload>/plugin`
	// without recursively walking hundreds of asset folders.
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		base := filepath.Join(root, e.Name())
		// Probe only the two conventional names rather than enumerating the full
		// contents of every Workshop asset subdirectory. This keeps discovery
		// cheap even with hundreds of subscribed items.
		add(filepath.Join(base, "plugin"))
		add(filepath.Join(base, "plugins"))
	}
	sort.Strings(out)
	return out
}

func findWorkshopMetadata(root string) string {
	candidates := []string{
		filepath.Join(root, "workshopconfig.ini"),
		filepath.Join(root, "WorkshopConfig.ini"),
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(root, e.Name(), "workshopconfig.ini")
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

func quotedDirective(text, directive string) string {
	lower := strings.ToLower(text)
	needle := strings.ToLower(directive)
	pos := strings.Index(lower, needle)
	if pos < 0 {
		return ""
	}
	rest := text[pos+len(directive):]
	first := strings.Index(rest, "\"")
	if first < 0 {
		return ""
	}
	rest = rest[first+1:]
	// ITEM_DESC is intentionally multiline. Its terminating quote is followed
	// by another $ directive or EOF in WRSR Workshop metadata.
	if strings.EqualFold(strings.TrimSpace(directive), "$ITEM_DESC") {
		end := strings.LastIndex(rest, "\"")
		if end >= 0 {
			return strings.TrimSpace(rest[:end])
		}
		return strings.TrimSpace(rest)
	}
	end := strings.Index(rest, "\"")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

func plainWorkshopDescription(text string) string {
	text = strings.ReplaceAll(text, "[*]", "• ")
	text = strings.ReplaceAll(text, "[br]", "\n")
	text = strings.ReplaceAll(text, "[BR]", "\n")
	text = workshopBBCode.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(out) > 0 && !blank {
				out = append(out, "")
				blank = true
			}
			continue
		}
		blank = false
		out = append(out, line)
		if len(strings.Join(out, "\n")) > 6000 {
			break
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func readWorkshopConfig(path string) (name, description, id string) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 2*1024*1024 {
		return "", "", ""
	}
	text := string(data)
	name = quotedDirective(text, "$ITEM_NAME")
	description = plainWorkshopDescription(quotedDirective(text, "$ITEM_DESC"))
	// ITEM_ID is not quoted in normal WRSR workshopconfig.ini files.
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 && strings.EqualFold(fields[0], "$ITEM_ID") && validWorkshopID(fields[1]) {
			id = fields[1]
			break
		}
	}
	return
}

func imageScore(name string) int {
	n := strings.ToLower(name)
	ext := strings.ToLower(filepath.Ext(n))
	if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".bmp" {
		return -1
	}
	score := 0
	if strings.Contains(n, "preview") {
		score += 100
	}
	if strings.Contains(n, "thumb") {
		score += 80
	}
	if strings.Contains(n, "workshop") {
		score += 40
	}
	if strings.Contains(n, "logo") {
		score += 20
	}
	return score
}

func findWorkshopThumbnail(root string) string {
	best, bestScore := "", -1
	var scan func(string, int)
	scan = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			if e.IsDir() {
				if depth < 1 {
					scan(p, depth+1)
				}
				continue
			}
			score := imageScore(e.Name())
			if score > bestScore {
				best, bestScore = p, score
			}
		}
	}
	scan(root, 0)
	if bestScore < 20 {
		return ""
	}
	return best
}

func workshopFolderEmpty(path string) bool {
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) == 0
}

func (item *WorkshopItem) recomputeUnsupported() {
	reasons := append([]string{}, item.LocalUnsupportedReasons...)
	if item.RemoteSupportState == "unavailable" {
		reason := strings.TrimSpace(item.RemoteSupportReason)
		if reason == "" {
			reason = "Workshop page is unavailable"
		}
		reasons = append(reasons, reason)
	}
	item.UnsupportedReasons = reasons
	item.Unsupported = len(reasons) > 0
}

func (item *WorkshopItem) applySupportRecord(record WorkshopSupportRecord) {
	item.RemoteSupportState = record.State
	item.RemoteSupportReason = record.Reason
	item.RemoteCheckedUnix = record.CheckedUnix
	item.recomputeUnsupported()
}

func discoverWorkshopItems(gamePath string, settings map[string]bool) ([]WorkshopItem, []string) {
	root := workshopRootFromGamePath(gamePath)
	if root == "" {
		return nil, []string{"Workshop: Steam library could not be derived from the selected WRSR path"}
	}
	dirs, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("Workshop: %s (%v)", root, err)}
	}
	items := make([]WorkshopItem, 0, len(dirs))
	warnings := []string{}
	for _, entry := range dirs {
		if !entry.IsDir() || !validWorkshopID(entry.Name()) {
			continue
		}
		folder := filepath.Join(root, entry.Name())
		item := WorkshopItem{ID: entry.Name(), Folder: folder, Name: "Workshop Item " + entry.Name(), Enabled: true}
		if v, ok := settings[item.ID]; ok {
			item.Enabled = v
		}
		item.ConfigPath = findWorkshopMetadata(folder)
		if item.ConfigPath != "" {
			name, desc, metaID := readWorkshopConfig(item.ConfigPath)
			if name != "" {
				item.Name = name
			}
			if desc != "" {
				item.Description = desc
			}
			if metaID != "" && metaID != item.ID {
				warnings = append(warnings, fmt.Sprintf("Workshop %s metadata declares ITEM_ID %s", item.ID, metaID))
			}
		}
		item.Thumbnail = findWorkshopThumbnail(folder)
		item.PluginDirs = findWorkshopPluginDirs(folder)
		if item.Description == "" {
			item.Description = "No local Workshop description metadata was found for this installed item."
		}
		if item.ConfigPath == "" {
			item.LocalUnsupportedReasons = append(item.LocalUnsupportedReasons, "No local Workshop metadata")
		}
		if item.Name == "" || item.Name == "Workshop Item "+item.ID {
			item.LocalUnsupportedReasons = append(item.LocalUnsupportedReasons, "No mod name")
		}
		if item.Thumbnail == "" {
			item.LocalUnsupportedReasons = append(item.LocalUnsupportedReasons, "No preview image")
		}
		if workshopFolderEmpty(folder) {
			item.LocalUnsupportedReasons = append(item.LocalUnsupportedReasons, "Workshop folder is empty")
		}
		item.recomputeUnsupported()
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := strings.ToLower(items[i].Name), strings.ToLower(items[j].Name)
		if a == b {
			ai, _ := strconv.ParseUint(items[i].ID, 10, 64)
			bi, _ := strconv.ParseUint(items[j].ID, 10, 64)
			return ai < bi
		}
		return a < b
	})
	return items, warnings
}

func workshopURL(id string) string {
	if !validWorkshopID(id) {
		return ""
	}
	return "https://steamcommunity.com/sharedfiles/filedetails/?id=" + id
}

func (a *application) releaseWorkshopImage() {
	if a.workshopImage != 0 {
		disposeGDIPlusImage(a.workshopImage)
	}
	a.workshopImage = 0
	a.workshopImagePath = ""
}

func (a *application) releasePluginImage() {
	if a.pluginImage != 0 {
		disposeGDIPlusImage(a.pluginImage)
	}
	a.pluginImage = 0
	a.pluginImagePath = ""
}

func (a *application) ensurePluginImage() uintptr {
	p := a.selectedPlugin()
	path := ""
	if p != nil && p.WorkshopID != "" {
		if item := a.workshopItemByID(p.WorkshopID); item != nil {
			path = item.Thumbnail
		}
	}
	if path == a.pluginImagePath {
		return a.pluginImage
	}
	a.releasePluginImage()
	if path == "" {
		return 0
	}
	a.pluginImagePath = path
	a.pluginImage = loadGDIPlusImage(path)
	return a.pluginImage
}
func (a *application) ensureWorkshopImage() uintptr {
	item := a.selectedWorkshopItem()
	path := ""
	if item != nil {
		path = item.Thumbnail
	}
	if path == a.workshopImagePath {
		return a.workshopImage
	}
	a.releaseWorkshopImage()
	if path == "" {
		return 0
	}
	// Remember failed loads too so repaint does not repeatedly reopen a bad file.
	a.workshopImagePath = path
	a.workshopImage = loadGDIPlusImage(path)
	return a.workshopImage
}
