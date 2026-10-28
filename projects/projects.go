package projects

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"evemaildiscord/config"
	"evemaildiscord/db"
	"evemaildiscord/esi"

	"github.com/bwmarrin/discordgo"
)

const (
	// forgeRegionID is The Forge market region used for price lookups.
	forgeRegionID = 10000002
	// compatibilityDate: the projects/freelance-jobs ESI routes only exist when
	// requests carry this X-Compatibility-Date (or newer).
	compatibilityDate = "2026-06-01"
	// tycoonStatsURL is the EVE Tycoon market stats API (https://evetycoon.com/docs).
	tycoonStatsURL = "https://evetycoon.com/api/v1/market/stats/%d/%d"

	maxPagesPerPoll = 5
)

// ESI state names treated as "finished".
var finishedStates = map[string]string{
	"Completed": "completed",
	"Expired":   "expired",
}

type finishedEntry struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	State        string `json:"state"`
	LastModified string `json:"last_modified"`
}

type cursor struct {
	After  string `json:"after"`
	Before string `json:"before"`
}

type listing struct {
	Cursor  *cursor         `json:"cursor"`
	Entries []finishedEntry `json:"-"`
}

func (l *listing) UnmarshalJSON(data []byte) error {
	var raw struct {
		Cursor *cursor `json:"cursor"`
		// Projects (corporations listing) and freelance_jobs (jobs listings)
		Projects      []finishedEntry `json:"projects"`
		FreelanceJobs []finishedEntry `json:"freelance_jobs"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	l.Cursor = raw.Cursor
	if raw.Projects != nil {
		l.Entries = raw.Projects
	} else {
		l.Entries = raw.FreelanceJobs
	}
	return nil
}

// PollProjects polls finished EVE projects and freelance jobs for every guild
// that configured one of the channels, every 2 minutes.
func PollProjects(dg *discordgo.Session) {
	CheckFinished(dg)
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		CheckFinished(dg)
	}
}

// CheckFinished posts newly finished projects and freelance jobs.
func CheckFinished(dg *discordgo.Session) {
	if dg == nil || db.DB == nil {
		return
	}

	rows, err := db.DB.Query("SELECT DISTINCT guild_id FROM config WHERE key = 'projects_channel' OR key = 'freelance_jobs_channel'")
	if err != nil {
		log.Printf("Error querying projects channels: %v", err)
		return
	}

	var guildIDs []string
	for rows.Next() {
		var gid string
		if rows.Scan(&gid) == nil && gid != "" {
			guildIDs = append(guildIDs, gid)
		}
	}
	_ = rows.Close()

	for _, gid := range guildIDs {
		processGuild(dg, gid)
	}
}

func processGuild(dg *discordgo.Session, guildID string) {
	integ, err := db.GetGuildIntegration(guildID)
	if err != nil {
		return
	}

	charID := integ.EveCharacterID
	if charID <= 0 {
		if envCharID := config.GetCharacterID(); envCharID != "" {
			charID, _ = strconv.Atoi(envCharID)
		}
	}
	if charID <= 0 {
		return
	}

	client := esi.GetOAuthHTTPClient(guildID)
	if client == nil {
		return
	}

	corpID, err := getCorporationID(charID)
	if err != nil || corpID <= 0 {
		log.Printf("[Guild %s] Error resolving corporation for character %d: %v", guildID, charID, err)
		return
	}

	for _, kind := range []string{"project", "freelance job"} {
		var cfgKey string
		if kind == "project" {
			cfgKey = "projects_channel"
		} else {
			cfgKey = "freelance_jobs_channel"
		}
		var chanID string
		if err := db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = ?", guildID, cfgKey).Scan(&chanID); err != nil || chanID == "" {
			continue
		}
		if err := processKind(dg, guildID, chanID, client, kind, corpID); err != nil {
			log.Printf("[Guild %s] Error processing %ss: %v", guildID, kind, err)
		}
	}
}

func processKind(dg *discordgo.Session, guildID, channelID string, client *http.Client, kind string, corpID int64) error {
	watermark, err := db.GetProjectsWatermark(guildID, kind)
	if err != nil {
		return err
	}

	var listURL string
	if kind == "project" {
		listURL = fmt.Sprintf("https://esi.evetech.net/corporations/%d/projects/?limit=100", corpID)
	} else {
		listURL = fmt.Sprintf("https://esi.evetech.net/corporations/%d/freelance-jobs/?limit=100", corpID)
	}

	// Walk newest-first pages, collecting finished entries newer than the watermark.
	var newFinished []finishedEntry
	maxSeen := watermark
	cursorBefore := ""
	wmTime := parseESITime(watermark)
	for page := 0; page < maxPagesPerPoll; page++ {
		var l listing
		pageURL := listURL
		if page > 0 {
			pageURL = fmt.Sprintf("%s&before=%s", listURL, url.QueryEscape(cursorBefore))
		}
		if err := esiGetJSON(client, pageURL, &l); err != nil {
			return err
		}
		olderThanWatermark := false
		for _, e := range l.Entries {
			t := parseESITime(e.LastModified)
			if t.After(parseESITime(maxSeen)) {
				maxSeen = e.LastModified
			}
			if !wmTime.IsZero() && !t.After(wmTime) {
				olderThanWatermark = true
				continue
			}
			if _, done := finishedStates[e.State]; done {
				newFinished = append(newFinished, e)
			}
		}
		if olderThanWatermark || l.Cursor == nil || l.Cursor.Before == "" {
			break
		}
		cursorBefore = l.Cursor.Before
	}

	// First poll: baseline only, like EVE mail.
	if watermark == "" {
		return db.SetProjectsWatermark(guildID, kind, formatESITime(parseESITime(maxSeen)))
	}

	// Oldest first so replies read chronologically.
	sort.Slice(newFinished, func(i, j int) bool { return parseESITime(newFinished[i].LastModified).Before(parseESITime(newFinished[j].LastModified)) })

	for _, e := range newFinished {
		if err := postFinished(dg, guildID, channelID, client, kind, corpID, e); err != nil {
			return err
		}
	}

	return db.SetProjectsWatermark(guildID, kind, formatESITime(parseESITime(maxSeen)))
}

// parseESITime parses an ESI last_modified timestamp, tolerating missing
// fractional seconds. Returns the zero time on failure.
func parseESITime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// formatESITime renders a timestamp for watermark storage in a stable format.
func formatESITime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func postFinished(dg *discordgo.Session, guildID, channelID string, client *http.Client, kind string, corpID int64, e finishedEntry) error {
	reason := finishedStates[e.State]

	var deliverables []deliverable
	var err error
	if kind == "project" {
		deliverables, err = projectDeliverables(client, corpID, e.ID)
	} else {
		deliverables, err = jobDeliverables(client, e.ID)
	}
	if err != nil {
		log.Printf("[Guild %s] Error fetching %s details for %s: %v", guildID, kind, e.ID, err)
		return err
	}

	msg := buildMessage(guildID, kind, e.Name, reason, deliverables)

	if _, err := dg.ChannelMessageSend(channelID, msg); err != nil {
		return err
	}
	log.Printf("[Guild %s] Posted finished %s %s (%s)", guildID, kind, e.Name, reason)
	return nil
}

func getCorporationID(charID int) (int64, error) {
	var char struct {
		CorporationID int64 `json:"corporation_id"`
	}
	resp, err := esi.Get(fmt.Sprintf("https://esi.evetech.net/v1/characters/%d/", charID))
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("esi returned status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&char); err != nil {
		return 0, err
	}
	return char.CorporationID, nil
}

// esiGetJSON performs a GET with the compatibility-date header required by the
// projects/freelance-jobs API and decodes the JSON response.
func esiGetJSON(client *http.Client, url string, out interface{}) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Compatibility-Date", compatibilityDate)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GET %s returned status %d: %s", url, resp.StatusCode, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type deliverable struct {
	TypeID  int64
	GroupID int64
	Name    string
}

// projectDeliverables returns the configured deliver items of a project, if any.
func projectDeliverables(client *http.Client, corpID int64, projectID string) ([]deliverable, error) {
	var detail struct {
		Configuration json.RawMessage `json:"configuration"`
	}
	url := fmt.Sprintf("https://esi.evetech.net/corporations/%d/projects/%s/", corpID, projectID)
	if err := esiGetJSON(client, url, &detail); err != nil {
		return nil, err
	}

	var cfg map[string]json.RawMessage
	if len(detail.Configuration) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(detail.Configuration, &cfg); err != nil {
		return nil, err
	}
	deliverItem, ok := cfg["deliver_item"]
	if !ok {
		return nil, nil // not a deliver-type project
	}
	var di struct {
		Items []struct {
			TypeID  *int64 `json:"type_id"`
			GroupID *int64 `json:"group_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(deliverItem, &di); err != nil {
		return nil, err
	}

	var out []deliverable
	for _, it := range di.Items {
		if it.TypeID != nil && *it.TypeID > 0 {
			out = append(out, deliverable{TypeID: *it.TypeID})
		} else if it.GroupID != nil && *it.GroupID > 0 {
			out = append(out, deliverable{GroupID: *it.GroupID})
		}
	}
	return resolveDeliverableNames(out)
}

// jobDeliverables returns the configured deliver items of a freelance job, if any.
func jobDeliverables(client *http.Client, jobID string) ([]deliverable, error) {
	var detail struct {
		Configuration struct {
			Parameters map[string]json.RawMessage `json:"parameters"`
		} `json:"configuration"`
	}
	url := fmt.Sprintf("https://esi.evetech.net/freelance-jobs/%s/", jobID)
	if err := esiGetJSON(client, url, &detail); err != nil {
		return nil, err
	}

	var out []deliverable
	for _, rawParam := range detail.Configuration.Parameters {
		var param struct {
			CorporationItemDelivery *struct {
				ItemType *struct {
					Values []struct {
						ValueType string   `json:"value_type"`
						Values    []string `json:"values"`
					} `json:"values"`
				} `json:"item_type"`
			} `json:"corporation_item_delivery"`
		}
		if err := json.Unmarshal(rawParam, &param); err != nil {
			continue
		}
		if param.CorporationItemDelivery == nil || param.CorporationItemDelivery.ItemType == nil {
			continue
		}
		for _, v := range param.CorporationItemDelivery.ItemType.Values {
			for _, idStr := range v.Values {
				id, err := strconv.ParseInt(idStr, 10, 64)
				if err != nil || id <= 0 {
					continue
				}
				switch v.ValueType {
				case "item_type":
					out = append(out, deliverable{TypeID: id})
				case "item_group":
					out = append(out, deliverable{GroupID: id})
				}
			}
		}
	}
	return resolveDeliverableNames(out)
}

// resolveDeliverableNames fills in item names for deliverables. Type names come
// from the universe names endpoint; group names from the groups endpoint.
func resolveDeliverableNames(items []deliverable) ([]deliverable, error) {
	if len(items) == 0 {
		return items, nil
	}

	var typeIDs []int64
	for _, it := range items {
		if it.TypeID > 0 {
			typeIDs = append(typeIDs, it.TypeID)
		}
	}
	names := make(map[int64]string)
	if len(typeIDs) > 0 {
		body, _ := json.Marshal(typeIDs)
		resp, err := esi.Post("https://esi.evetech.net/v3/universe/names/", "application/json", bytes.NewReader(body))
		if err == nil {
			var resolved []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			}
			err = json.NewDecoder(resp.Body).Decode(&resolved)
			_ = resp.Body.Close()
			if err == nil {
				for _, r := range resolved {
					names[r.ID] = r.Name
				}
			}
		}
	}

	for i, it := range items {
		if it.TypeID > 0 {
			items[i].Name = names[it.TypeID]
		} else if it.GroupID > 0 {
			var g struct {
				Name string `json:"name"`
			}
			if err := fetchJSONPublic(fmt.Sprintf("https://esi.evetech.net/v1/universe/groups/%d/", it.GroupID), &g); err == nil {
				items[i].Name = g.Name
			}
		}
	}
	return items, nil
}

func fetchJSONPublic(url string, out interface{}) error {
	resp, err := esi.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return fmt.Errorf("GET %s returned status %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type marketStats struct {
	BuyAvgFivePercent float64 `json:"buyAvgFivePercent"`
}

// fetchBuyAvg returns the current average buy price (top 5% of buy orders)
// for an item type in The Forge, from the EVE Tycoon API.
func fetchBuyAvg(typeID int64) (float64, error) {
	var stats marketStats
	url := fmt.Sprintf(tycoonStatsURL, forgeRegionID, typeID)
	resp, err := esi.Get(url)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("eve-tycoon returned status %d for type %d", resp.StatusCode, typeID)
	}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return 0, err
	}
	return stats.BuyAvgFivePercent, nil
}

// getBuybackPercentage returns the configured buyback percentage for a category, or 0.
func getBuybackPercentage(guildID, category string) float64 {
	if category == "" {
		return 0
	}
	var raw string
	if err := db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = ?", guildID, "buyback_"+category).Scan(&raw); err != nil {
		return 0
	}
	pct, err := strconv.ParseFloat(raw, 64)
	if err != nil || pct <= 0 {
		return 0
	}
	return pct
}

func formatISK(v float64) string {
	return fmt.Sprintf("%.2f", v)
}

// buildMessage composes the Discord post for a finished project / freelance job.
// For deliver-type items it adds a market link, the current average buy price
// (The Forge) and, when a buyback percentage is configured for the item's
// category, the resulting buyback price.
func buildMessage(guildID, kind, name, reason string, deliverables []deliverable) string {
	var b strings.Builder
	fmt.Fprintf(&b, "✅ **%s finished (%s)** — %s", kind, reason, name)
	for _, d := range deliverables {
		b.WriteString("\nDeliver: ")
		if d.TypeID > 0 {
			if d.Name == "" {
				fmt.Fprintf(&b, "[item %d](https://evetycoon.com/market/%d)", d.TypeID, d.TypeID)
			} else {
				fmt.Fprintf(&b, "[%s](https://evetycoon.com/market/%d)", d.Name, d.TypeID)
			}
			if avgBuy, err := fetchBuyAvg(d.TypeID); err == nil && avgBuy > 0 {
				fmt.Fprintf(&b, " — avg buy (The Forge): %s ISK", formatISK(avgBuy))
				if pct := getBuybackPercentage(guildID, CategoryForName(d.Name)); pct > 0 {
					fmt.Fprintf(&b, " — buyback @ %s: %s ISK", formatPercent(pct), formatISK(avgBuy*pct))
				}
			}
		} else if d.GroupID > 0 {
			// Group-based deliveries have no single market item, so no link or price.
			if d.Name == "" {
				fmt.Fprintf(&b, "item group %d", d.GroupID)
			} else {
				b.WriteString(d.Name)
			}
			b.WriteString(" (item group)")
		}
	}
	return b.String()
}

// formatPercent renders a buyback fraction (0.5 -> "50%").
func formatPercent(pct float64) string {
	return fmt.Sprintf("%g%%", pct*100)
}