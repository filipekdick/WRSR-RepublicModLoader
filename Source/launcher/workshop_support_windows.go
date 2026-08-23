//go:build windows

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const workshopSupportCacheSchema = 1
const workshopSupportTTL = 12 * time.Hour

type WorkshopSupportRecord struct {
	State       string `json:"state"` // ok, unavailable, unknown
	Reason      string `json:"reason,omitempty"`
	CheckedUnix int64  `json:"checkedUnix"`
}

type workshopSupportCacheFile struct {
	SchemaVersion int                              `json:"schemaVersion"`
	Records       map[string]WorkshopSupportRecord `json:"records"`
}

type workshopSupportCompletion struct {
	Records map[string]WorkshopSupportRecord
	Checked int
}

func workshopSupportCachePath(knowledgeRoot string) string {
	return filepath.Join(knowledgeRoot, "workshop", "support-status.json")
}

func loadWorkshopSupportCache(knowledgeRoot string) map[string]WorkshopSupportRecord {
	out := map[string]WorkshopSupportRecord{}
	path := workshopSupportCachePath(knowledgeRoot)
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 2*1024*1024 {
		return out
	}
	var file workshopSupportCacheFile
	if json.Unmarshal(data, &file) != nil || file.SchemaVersion != workshopSupportCacheSchema || file.Records == nil {
		return out
	}
	for id, record := range file.Records {
		if validWorkshopID(id) && (record.State == "ok" || record.State == "unavailable" || record.State == "unknown") {
			out[id] = record
		}
	}
	return out
}

func saveWorkshopSupportCache(knowledgeRoot string, records map[string]WorkshopSupportRecord) {
	if records == nil {
		return
	}
	path := workshopSupportCachePath(knowledgeRoot)
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	file := workshopSupportCacheFile{SchemaVersion: workshopSupportCacheSchema, Records: records}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o644) != nil {
		return
	}
	_ = os.Remove(path)
	_ = os.Rename(tmp, path)
}

func workshopSupportRecordFresh(record WorkshopSupportRecord, now time.Time) bool {
	if record.CheckedUnix <= 0 {
		return false
	}
	age := now.Sub(time.Unix(record.CheckedUnix, 0))
	return age >= 0 && age < workshopSupportTTL
}

func checkWorkshopPage(client *http.Client, id string) WorkshopSupportRecord {
	checked := time.Now().Unix()
	url := workshopURL(id)
	if url == "" {
		return WorkshopSupportRecord{State: "unavailable", Reason: "Invalid Workshop URL", CheckedUnix: checked}
	}
	req, err := http.NewRequest(http.MethodGet, url+"&l=english", nil)
	if err != nil {
		return WorkshopSupportRecord{State: "unknown", Reason: "Workshop URL could not be checked", CheckedUnix: checked}
	}
	req.Header.Set("User-Agent", "RepublicModLoader/"+launcherVersion)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := client.Do(req)
	if err != nil {
		// A network outage or transient Steam failure is not evidence that the
		// Workshop item itself is dead. Keep it unknown instead of producing a
		// false unsupported result.
		return WorkshopSupportRecord{State: "unknown", Reason: "Workshop page could not be verified", CheckedUnix: checked}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return WorkshopSupportRecord{State: "unavailable", Reason: "Workshop page is unavailable", CheckedUnix: checked}
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return WorkshopSupportRecord{State: "unknown", Reason: "Steam temporarily refused the Workshop check", CheckedUnix: checked}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return WorkshopSupportRecord{State: "unknown", Reason: "Workshop page could not be verified", CheckedUnix: checked}
	}
	lower := strings.ToLower(string(body))
	errorTitle := strings.Contains(lower, "<title>steam community :: error</title>")
	accessError := strings.Contains(lower, "there was a problem accessing the item. please try again")
	removed := strings.Contains(lower, "this item has been removed from the community")
	if removed || (errorTitle && accessError) {
		reason := "Workshop item appears delisted or inaccessible"
		if removed {
			reason = "Workshop item has been removed from the community"
		}
		return WorkshopSupportRecord{State: "unavailable", Reason: reason, CheckedUnix: checked}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && (strings.Contains(lower, "workshopitemtitle") || strings.Contains(lower, "workshopitemdetailsheader")) {
		return WorkshopSupportRecord{State: "ok", CheckedUnix: checked}
	}
	return WorkshopSupportRecord{State: "unknown", Reason: "Workshop page returned an unrecognized response", CheckedUnix: checked}
}

func scanWorkshopSupport(ids []string) workshopSupportCompletion {
	completion := workshopSupportCompletion{Records: map[string]WorkshopSupportRecord{}}
	if len(ids) == 0 {
		return completion
	}
	transport := &http.Transport{
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     20 * time.Second,
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: transport}
	defer transport.CloseIdleConnections()

	const workers = 8
	jobs := make(chan string)
	results := make(chan struct {
		id     string
		record WorkshopSupportRecord
	}, len(ids))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				results <- struct {
					id     string
					record WorkshopSupportRecord
				}{id: id, record: checkWorkshopPage(client, id)}
				// Avoid hammering Steam Community when hundreds of installed
				// items are checked in one pass.
				time.Sleep(70 * time.Millisecond)
			}
		}()
	}
	go func() {
		for _, id := range ids {
			jobs <- id
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	for result := range results {
		completion.Records[result.id] = result.record
		completion.Checked++
	}
	return completion
}
