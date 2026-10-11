package donations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"evemaildiscord/db"
	"evemaildiscord/esi"

	"github.com/bwmarrin/discordgo"
)

type EsiWalletJournalEntry struct {
	ID            int64     `json:"id"`
	Date          time.Time `json:"date"`
	RefType       string    `json:"ref_type"`
	FirstPartyID  int       `json:"first_party_id"`
	SecondPartyID int       `json:"second_party_id"`
	Amount        float64   `json:"amount"`
	Balance       float64   `json:"balance,omitempty"`
	Description   string    `json:"description,omitempty"`
	Reason        string    `json:"reason,omitempty"`
}

type entityInfo struct {
	Name     string
	Category string
}

var (
	entityCache sync.Map
)

func SanitizeText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '-' || r == '—' || r == '–' {
			b.WriteRune(' ')
			continue
		}
		if isEmoji(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isEmoji(r rune) bool {
	if unicode.In(r, unicode.So, unicode.Sk) {
		return true
	}
	if r >= 0x1F600 && r <= 0x1F64F {
		return true
	}
	if r >= 0x1F300 && r <= 0x1F5FF {
		return true
	}
	if r >= 0x1F680 && r <= 0x1F6FF {
		return true
	}
	if r >= 0x1F1E0 && r <= 0x1F1FF {
		return true
	}
	if r >= 0x2600 && r <= 0x26FF {
		return true
	}
	if r >= 0x2700 && r <= 0x27BF {
		return true
	}
	if r >= 0xFE00 && r <= 0xFE0F {
		return true
	}
	if r >= 0x1F900 && r <= 0x1F9FF {
		return true
	}
	if r >= 0x1FA70 && r <= 0x1FAFF {
		return true
	}
	return false
}

func FormatISK(amount float64) string {
	intPart := int64(amount)
	if intPart < 0 {
		intPart = 0
	}
	fracPart := amount - float64(intPart)

	str := strconv.FormatInt(intPart, 10)
	var b strings.Builder
	n := len(str)
	for i, c := range str {
		if i > 0 && (n-i)%3 == 0 {
			b.WriteRune(',')
		}
		b.WriteRune(c)
	}

	if fracPart >= 0.01 {
		fracStr := fmt.Sprintf("%.2f", fracPart)
		if len(fracStr) > 1 {
			b.WriteString(fracStr[1:])
		}
	}

	b.WriteString(" ISK")
	return SanitizeText(b.String())
}

func FetchCorpName(corpID int) string {
	resp, err := esi.Get(fmt.Sprintf("https://esi.evetech.net/v4/corporations/%d/", corpID))
	if err == nil && resp != nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		var info struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&info); err == nil && info.Name != "" {
			clean := SanitizeText(info.Name)
			SetCachedEntity(corpID, clean, "corporation")
			return clean
		}
	} else if resp != nil {
		_ = resp.Body.Close()
	}
	return ""
}

func FetchCharName(charID int) string {
	resp, err := esi.Get(fmt.Sprintf("https://esi.evetech.net/latest/characters/%d/", charID))
	if err == nil && resp != nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		var info struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&info); err == nil && info.Name != "" {
			clean := SanitizeText(info.Name)
			SetCachedEntity(charID, clean, "character")
			return clean
		}
	} else if resp != nil {
		_ = resp.Body.Close()
	}
	return ""
}

func GetEntityInfo(entityID int) (string, string) {
	if val, ok := entityCache.Load(entityID); ok {
		info := val.(entityInfo)
		if info.Name != "" &&
			!strings.HasPrefix(info.Name, "Entity ") &&
			!strings.HasPrefix(info.Name, "Corporation ") &&
			!strings.HasPrefix(info.Name, "Character ") {
			return info.Name, info.Category
		}
	}

	hintCategory := ""
	var discordID string
	if err := db.DB.QueryRow("SELECT discord_id FROM character_to_discord WHERE eve_id = ?", entityID).Scan(&discordID); err == nil {
		hintCategory = "character"
	} else {
		var roleID string
		if err := db.DB.QueryRow("SELECT role_id FROM corp_to_role WHERE corp_id = ?", entityID).Scan(&roleID); err == nil {
			hintCategory = "corporation"
		}
	}

	bodyBytes, err := json.Marshal([]int{entityID})
	if err == nil {
		resp, respErr := esi.Post("https://esi.evetech.net/latest/universe/names/", "application/json", bytes.NewBuffer(bodyBytes))
		if respErr == nil && resp != nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var names []struct {
					ID       int    `json:"id"`
					Name     string `json:"name"`
					Category string `json:"category"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&names); err == nil && len(names) > 0 {
					name := SanitizeText(names[0].Name)
					category := names[0].Category
					info := entityInfo{Name: name, Category: category}
					entityCache.Store(entityID, info)
					return info.Name, info.Category
				}
			}
		}
	}

	if hintCategory == "corporation" {
		if cName := FetchCorpName(entityID); cName != "" {
			return cName, "corporation"
		}
	} else if hintCategory == "character" {
		if chName := FetchCharName(entityID); chName != "" {
			return chName, "character"
		}
	}

	if cName := FetchCorpName(entityID); cName != "" {
		return cName, "corporation"
	}
	if chName := FetchCharName(entityID); chName != "" {
		return chName, "character"
	}

	fallbackCategory := "character"
	if hintCategory != "" {
		fallbackCategory = hintCategory
	}
	fallbackName := fmt.Sprintf("Entity %d", entityID)
	if fallbackCategory == "corporation" {
		fallbackName = fmt.Sprintf("Corporation %d", entityID)
	}
	info := entityInfo{Name: fallbackName, Category: fallbackCategory}
	entityCache.Store(entityID, info)
	return info.Name, info.Category
}

func SetCachedEntity(id int, name, category string) {
	entityCache.Store(id, entityInfo{Name: SanitizeText(name), Category: category})
}

func GetTrackedCorpDisplayName(guildID string, roleID string, eveCorpID int, s ...*discordgo.Session) string {
	name, _ := GetEntityInfo(eveCorpID)
	if name != "" && !strings.HasPrefix(name, "Entity ") && !strings.HasPrefix(name, "Corporation ") {
		return SanitizeText(name)
	}

	if cName := FetchCorpName(eveCorpID); cName != "" {
		return SanitizeText(cName)
	}

	if len(s) > 0 && s[0] != nil && guildID != "" && roleID != "" {
		if s[0].State != nil {
			if g, err := s[0].State.Guild(guildID); err == nil && g != nil {
				for _, r := range g.Roles {
					if r.ID == roleID && r.Name != "" && !strings.EqualFold(r.Name, "@everyone") {
						clean := SanitizeText(r.Name)
						SetCachedEntity(eveCorpID, clean, "corporation")
						return clean
					}
				}
			}
		}
		if s[0].Ratelimiter != nil {
			if rolesList, err := s[0].GuildRoles(guildID); err == nil {
				for _, r := range rolesList {
					if r.ID == roleID && r.Name != "" && !strings.EqualFold(r.Name, "@everyone") {
						clean := SanitizeText(r.Name)
						SetCachedEntity(eveCorpID, clean, "corporation")
						return clean
					}
				}
			}
		}
	}

	if name != "" {
		return SanitizeText(name)
	}
	return fmt.Sprintf("Corporation %d", eveCorpID)
}

func ResolveEntity(guildID string, entityID int, entityType string) string {
	if entityType == "" {
		_, cat := GetEntityInfo(entityID)
		entityType = cat
	}

	if entityType == "corporation" {
		var roleID string
		err := db.DB.QueryRow("SELECT role_id FROM corp_to_role WHERE guild_id = ? AND corp_id = ?", guildID, entityID).Scan(&roleID)
		if err == nil && roleID != "" {
			return fmt.Sprintf("<@&%s>", roleID)
		}
		name, _ := GetEntityInfo(entityID)
		if name == "" || strings.HasPrefix(name, "Entity ") || strings.HasPrefix(name, "Corporation ") {
			if cName := FetchCorpName(entityID); cName != "" {
				name = cName
			} else {
				name = fmt.Sprintf("Corporation %d", entityID)
			}
		}
		name = SanitizeText(name)
		return fmt.Sprintf("[%s](https://evewho.com/corporation/%d)", name, entityID)
	}

	var discordID string
	err := db.DB.QueryRow("SELECT discord_id FROM character_to_discord WHERE eve_id = ?", entityID).Scan(&discordID)
	if err == nil && discordID != "" {
		return fmt.Sprintf("<@%s>", discordID)
	}

	name, _ := GetEntityInfo(entityID)
	if name == "" || strings.HasPrefix(name, "Entity ") || strings.HasPrefix(name, "Character ") {
		if chName := FetchCharName(entityID); chName != "" {
			name = chName
		} else {
			name = fmt.Sprintf("Character %d", entityID)
		}
	}
	name = SanitizeText(name)
	return fmt.Sprintf("[%s](https://evewho.com/character/%d)", name, entityID)
}

func FormatDonationAnnouncement(guildID string, donorID int, donorType string, amount float64) string {
	resolved := ResolveEntity(guildID, donorID, donorType)
	amountStr := FormatISK(amount)
	msg := fmt.Sprintf("Thank you to %s for the generous donation of %s!", resolved, amountStr)
	return SanitizeText(msg)
}

func BuildLeaderboard(guildID string, s ...*discordgo.Session) (string, error) {
	entries, err := db.GetDonationsLeaderboard(guildID, 50)
	if err != nil {
		return "", err
	}

	var corpNames []string
	if tracked, errTracked := db.GetTrackedCorporationsForGuild(guildID); errTracked == nil {
		for _, tc := range tracked {
			cName := GetTrackedCorpDisplayName(guildID, tc.RoleID, tc.EveCorpID, s...)
			corpNames = append(corpNames, cName)
		}
	}

	title := "**Top 50 Donors All Time**"
	if len(corpNames) == 1 {
		title = fmt.Sprintf("**Top 50 Donors All Time for %s**", corpNames[0])
	} else if len(corpNames) > 1 {
		title = fmt.Sprintf("**Top 50 Donors All Time for %s**", strings.Join(corpNames, ", "))
	}

	if len(entries) == 0 {
		return SanitizeText(fmt.Sprintf("%s\n\nNo donations recorded yet for tracked corporations.", title)), nil
	}

	var lines []string
	lines = append(lines, title+"\n")
	for idx, entry := range entries {
		resolved := ResolveEntity(guildID, entry.DonorID, entry.DonorType)
		amountStr := FormatISK(entry.TotalAmount)
		countStr := fmt.Sprintf("(%d donations)", entry.DonationCount)
		if entry.DonationCount == 1 {
			countStr = "(1 donation)"
		}
		line := fmt.Sprintf("%d. %s * %s %s", idx+1, resolved, amountStr, countStr)
		lines = append(lines, line)
	}

	return SanitizeText(strings.Join(lines, "\n")), nil
}

func FetchCorpWalletJournalPage(client *http.Client, corpID int, division int, page int) ([]EsiWalletJournalEntry, error) {
	if client == nil {
		return nil, fmt.Errorf("http client is nil")
	}
	if division <= 0 {
		division = 1
	}
	if page <= 0 {
		page = 1
	}
	url := fmt.Sprintf("https://esi.evetech.net/latest/corporations/%d/wallets/%d/journal/?page=%d", corpID, division, page)
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ESI returned status %d for corp %d wallet %d page %d", resp.StatusCode, corpID, division, page)
	}

	var entries []EsiWalletJournalEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("failed to decode wallet journal: %w", err)
	}
	return entries, nil
}

func FetchCorpWalletJournal(client *http.Client, corpID int, division int) ([]EsiWalletJournalEntry, error) {
	var allEntries []EsiWalletJournalEntry
	for page := 1; page <= 50; page++ {
		entries, err := FetchCorpWalletJournalPage(client, corpID, division, page)
		if err != nil {
			if page == 1 {
				return nil, err
			}
			break
		}
		if len(entries) == 0 {
			break
		}
		allEntries = append(allEntries, entries...)
		if len(entries) < 2500 {
			break
		}
	}
	return allEntries, nil
}

func PollDonations(dg *discordgo.Session) {
	CheckDonations(dg)
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		CheckDonations(dg)
	}
}

var bannedPhrases = []string{
	"[bpc]",
}

func IsDonationBanned(reason string) bool {
	reasonLower := strings.ToLower(reason)
	for _, phrase := range bannedPhrases {
		if strings.Contains(reasonLower, phrase) {
			return true
		}
	}
	return false
}

func CheckDonations(dg *discordgo.Session) {
	if dg == nil || db.DB == nil {
		return
	}

	corpIDs, err := db.GetAllUniqueTrackedCorpIDs()
	if err != nil {
		log.Printf("[Donations] Error querying tracked corporations: %v", err)
		return
	}

	if len(corpIDs) == 0 {
		return
	}

	for _, corpID := range corpIDs {
		guildIDs, err := db.GetGuildsTrackingCorp(corpID)
		if err != nil || len(guildIDs) == 0 {
			continue
		}

		var client *http.Client
		for _, guildID := range guildIDs {
			c := esi.GetOAuthHTTPClient(guildID)
			if c != nil {
				client = c
				break
			}
		}
		if client == nil {
			log.Printf("[Donations] No valid OAuth HTTP client available to poll corp %d", corpID)
			continue
		}

		for division := 1; division <= 7; division++ {
			entries, fetchErr := FetchCorpWalletJournal(client, corpID, division)
			if fetchErr != nil {
				if strings.Contains(fetchErr.Error(), "status 403") {
					log.Printf("[Donations] ESI returned status 403 for corp %d wallet %d: character token might be missing esi-wallet.read_corporation_wallets.v1 scope or character lacks Accountant/Director roles in-game", corpID, division)
					break
				}
				continue
			}

			if len(entries) == 0 {
				continue
			}

			for i := len(entries) - 1; i >= 0; i-- {
				entry := entries[i]
				if entry.RefType != "player_donation" && entry.RefType != "corporation_donation" {
					continue
				}
				if entry.Amount <= 0 || entry.FirstPartyID <= 0 {
					continue
				}
				if IsDonationBanned(entry.Reason) {
					continue
				}

				exists, err := db.DonationRecordExists(entry.ID)
				if err != nil || exists {
					continue
				}

				match, err := db.DonationExists(corpID, entry.FirstPartyID, entry.Amount, entry.Balance, entry.Date)
				if err == nil && match {
					// Already exists from historical paste import, link ESI transaction ID & update balance
					if db.DB != nil {
						_, _ = db.DB.Exec("UPDATE DonationRecord SET transaction_id = ?, balance = ? WHERE receiver_corp_id = ? AND donor_id = ? AND ABS(amount - ?) < 0.01", entry.ID, entry.Balance, corpID, entry.FirstPartyID, entry.Amount)
					}
					continue
				}

				_, category := GetEntityInfo(entry.FirstPartyID)
				donorType := "character"
				if category == "corporation" {
					donorType = "corporation"
				}

				rec := db.DonationRecord{
					TransactionID:  entry.ID,
					ReceiverCorpID: corpID,
					DonorID:        entry.FirstPartyID,
					DonorType:      donorType,
					Amount:         entry.Amount,
					Balance:        entry.Balance,
					Date:           entry.Date,
				}

				if err := db.InsertDonationRecord(rec); err != nil {
					log.Printf("[Donations] Error saving donation record %d: %v", entry.ID, err)
					continue
				}

				for _, guildID := range guildIDs {
					settings, err := db.GetGuildDonationSettings(guildID)
					if err != nil || settings == nil || settings.ChannelID == nil || *settings.ChannelID == "" {
						continue
					}

					msg := FormatDonationAnnouncement(guildID, entry.FirstPartyID, donorType, entry.Amount)
					_, sendErr := dg.ChannelMessageSend(*settings.ChannelID, msg)
					if sendErr != nil {
						log.Printf("[Donations] Error posting donation announcement to channel %s in guild %s: %v", *settings.ChannelID, guildID, sendErr)
					}
				}
			}
		}
	}
}

type ParsedWalletEntry struct {
	Date      time.Time
	Amount    float64
	Balance   float64
	DonorName string
	CorpName  string
}

func ParseWalletNumber(s string) (float64, error) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "ISK"))
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}

	hasComma := strings.Contains(s, ",")
	hasDot := strings.Contains(s, ".")

	if hasComma && hasDot {
		lastComma := strings.LastIndex(s, ",")
		lastDot := strings.LastIndex(s, ".")
		if lastComma > lastDot {
			s = strings.ReplaceAll(s, ".", "")
			s = strings.ReplaceAll(s, ",", ".")
		} else {
			s = strings.ReplaceAll(s, ",", "")
		}
	} else if hasComma {
		parts := strings.Split(s, ",")
		if len(parts) > 1 && len(parts[len(parts)-1]) == 3 {
			s = strings.ReplaceAll(s, ",", "")
		} else if len(parts) == 2 && len(parts[1]) != 3 {
			s = parts[0] + "." + parts[1]
		} else {
			s = strings.ReplaceAll(s, ",", "")
		}
	} else if hasDot {
		parts := strings.Split(s, ".")
		if len(parts) > 1 && len(parts[len(parts)-1]) == 3 {
			s = strings.ReplaceAll(s, ".", "")
		}
	}

	return strconv.ParseFloat(s, 64)
}

func ParseWalletDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	formats := []string{
		"2006.01.02 15:04",
		"2006-01-02 15:04",
		"2006/01/02 15:04",
		"2006.01.02 15:04:05",
		"2006-01-02 15:04:05",
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, s, time.UTC); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unknown date format: %q", s)
}

var (
	reDatePattern = regexp.MustCompile(`\d{4}[./-]\d{2}[./-]\d{2}\s+\d{2}:\d{2}(?::\d{2})?`)
	rePrefixTag   = regexp.MustCompile(`^\[[a-zA-Z0-9_-]+\]\s*`)
)

func ParseWalletJournalText(rawText string) []ParsedWalletEntry {
	var results []ParsedWalletEntry

	locs := reDatePattern.FindAllStringIndex(rawText, -1)
	for i := 0; i < len(locs); i++ {
		start := locs[i][0]
		end := len(rawText)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		chunk := strings.TrimSpace(rawText[start:end])
		if !strings.Contains(chunk, "Player Donation") {
			continue
		}

		parts := strings.Split(chunk, "\t")
		var dateStr, amountStr, balanceStr, desc string

		if len(parts) >= 4 {
			// Tab-separated chunk: Date \t Player Donation \t Amount \t Balance \t Desc
			for _, p := range parts {
				trimmed := strings.TrimSpace(p)
				if strings.EqualFold(trimmed, "Player Donation") {
					continue
				}
				if dateStr == "" {
					if _, err := ParseWalletDate(trimmed); err == nil {
						dateStr = trimmed
						continue
					}
				}
				if strings.HasSuffix(trimmed, "ISK") {
					if amountStr == "" {
						amountStr = trimmed
					} else if balanceStr == "" {
						balanceStr = trimmed
					}
					continue
				}
				if strings.Contains(trimmed, "deposited cash into") || desc == "" {
					desc = trimmed
				}
			}
		} else {
			// Space/tab mixed chunk: e.g. "2026.09.05 20:05 Player Donation 500.000.000 ISK 7.782.693.407 ISK Amayah Thara..."
			pdIdx := strings.Index(chunk, "Player Donation")
			if pdIdx != -1 {
				dateStr = strings.TrimSpace(chunk[:pdIdx])
				afterPD := strings.TrimSpace(chunk[pdIdx+len("Player Donation"):])
				iskIdx1 := strings.Index(afterPD, "ISK")
				if iskIdx1 != -1 {
					amountStr = strings.TrimSpace(afterPD[:iskIdx1])
					afterAmount := strings.TrimSpace(afterPD[iskIdx1+len("ISK"):])
					iskIdx2 := strings.Index(afterAmount, "ISK")
					if iskIdx2 != -1 {
						balanceStr = strings.TrimSpace(afterAmount[:iskIdx2])
						desc = strings.TrimSpace(afterAmount[iskIdx2+len("ISK"):])
					} else {
						desc = afterAmount
					}
				}
			}
		}

		if dateStr == "" || amountStr == "" {
			continue
		}

		date, err := ParseWalletDate(dateStr)
		if err != nil {
			continue
		}

		amount, err := ParseWalletNumber(amountStr)
		if err != nil {
			continue
		}

		balance, _ := ParseWalletNumber(balanceStr)
		cleanDesc := rePrefixTag.ReplaceAllString(strings.TrimSpace(desc), "")
		var donorName, corpName string
		if idx := strings.Index(cleanDesc, " deposited cash into "); idx != -1 {
			donorName = strings.TrimSpace(cleanDesc[:idx])
			after := cleanDesc[idx+len(" deposited cash into "):]
			corpName = strings.TrimSuffix(strings.TrimSpace(after), "'s account")
			corpName = strings.TrimSuffix(corpName, "'s account.")
			corpName = strings.TrimSuffix(corpName, " account")
			corpName = strings.TrimSpace(corpName)
		} else {
			donorName = strings.TrimSpace(cleanDesc)
		}

		if donorName != "" {
			results = append(results, ParsedWalletEntry{
				Date:      date,
				Amount:    amount,
				Balance:   balance,
				DonorName: donorName,
				CorpName:  corpName,
			})
		}
	}

	if len(results) > 0 {
		return results
	}

	// Line-by-line fallback
	lines := strings.Split(rawText, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "Player Donation") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}

		var dateStr, amountStr, balanceStr, desc string
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if strings.EqualFold(trimmed, "Player Donation") {
				continue
			}
			if dateStr == "" {
				if _, err := ParseWalletDate(trimmed); err == nil {
					dateStr = trimmed
					continue
				}
			}
			if strings.HasSuffix(trimmed, "ISK") {
				if amountStr == "" {
					amountStr = trimmed
				} else if balanceStr == "" {
					balanceStr = trimmed
				}
				continue
			}
			if strings.Contains(trimmed, "deposited cash into") || desc == "" {
				desc = trimmed
			}
		}

		if dateStr == "" || amountStr == "" {
			continue
		}

		date, err := ParseWalletDate(dateStr)
		if err != nil {
			continue
		}

		amount, err := ParseWalletNumber(amountStr)
		if err != nil {
			continue
		}

		balance, _ := ParseWalletNumber(balanceStr)
		cleanDesc := rePrefixTag.ReplaceAllString(strings.TrimSpace(desc), "")
		var donorName, corpName string
		if idx := strings.Index(cleanDesc, " deposited cash into "); idx != -1 {
			donorName = strings.TrimSpace(cleanDesc[:idx])
			after := cleanDesc[idx+len(" deposited cash into "):]
			corpName = strings.TrimSuffix(strings.TrimSpace(after), "'s account")
			corpName = strings.TrimSuffix(corpName, "'s account.")
			corpName = strings.TrimSuffix(corpName, " account")
			corpName = strings.TrimSpace(corpName)
		} else {
			donorName = strings.TrimSpace(cleanDesc)
		}

		if donorName != "" {
			results = append(results, ParsedWalletEntry{
				Date:      date,
				Amount:    amount,
				Balance:   balance,
				DonorName: donorName,
				CorpName:  corpName,
			})
		}
	}

	return results
}

func ResolveUniverseNames(names []string) (map[string]int, map[string]string, error) {
	nameToID := make(map[string]int)
	nameToType := make(map[string]string)
	if len(names) == 0 {
		return nameToID, nameToType, nil
	}

	uniqueMap := make(map[string]string)
	for _, n := range names {
		trimmed := strings.TrimSpace(n)
		if trimmed != "" {
			uniqueMap[strings.ToLower(trimmed)] = trimmed
		}
	}

	var uniqueList []string
	for _, orig := range uniqueMap {
		uniqueList = append(uniqueList, orig)
	}

	chunkSize := 500
	for i := 0; i < len(uniqueList); i += chunkSize {
		end := i + chunkSize
		if end > len(uniqueList) {
			end = len(uniqueList)
		}
		chunk := uniqueList[i:end]

		bodyBytes, err := json.Marshal(chunk)
		if err != nil {
			continue
		}

		resp, err := esi.Post("https://esi.evetech.net/latest/universe/ids/", "application/json", bytes.NewBuffer(bodyBytes))
		if err != nil || resp == nil {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			continue
		}

		var result struct {
			Characters []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"characters"`
			Corporations []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"corporations"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
			for _, c := range result.Characters {
				nameToID[strings.ToLower(c.Name)] = c.ID
				nameToType[strings.ToLower(c.Name)] = "character"
				SetCachedEntity(c.ID, c.Name, "character")
			}
			for _, c := range result.Corporations {
				nameToID[strings.ToLower(c.Name)] = c.ID
				nameToType[strings.ToLower(c.Name)] = "corporation"
				SetCachedEntity(c.ID, c.Name, "corporation")
			}
		}
		_ = resp.Body.Close()
	}

	return nameToID, nameToType, nil
}

func ImportWalletJournal(guildID string, targetCorpID int, rawText string) (int, int, string, error) {
	entries := ParseWalletJournalText(rawText)
	if len(entries) == 0 {
		return 0, 0, "", fmt.Errorf("no valid 'Player Donation' entries found in pasted text")
	}

	receiverCorpID := targetCorpID
	if receiverCorpID <= 0 {
		tracked, err := db.GetTrackedCorporationsForGuild(guildID)
		if err != nil {
			return 0, 0, "", fmt.Errorf("database error checking tracked corporations: %w", err)
		}
		if len(tracked) == 0 {
			return 0, 0, "", fmt.Errorf("no tracked corporations configured for this server. Use /set_donations_track_corporation first")
		}

		// Check if first entry CorpName matches any tracked corporation
		if entries[0].CorpName != "" {
			for _, tc := range tracked {
				infoName := GetTrackedCorpDisplayName(guildID, tc.RoleID, tc.EveCorpID)
				if strings.EqualFold(infoName, entries[0].CorpName) {
					receiverCorpID = tc.EveCorpID
					break
				}
			}
			if receiverCorpID <= 0 {
				resMap, _, _ := ResolveUniverseNames([]string{entries[0].CorpName})
				if cid, ok := resMap[strings.ToLower(entries[0].CorpName)]; ok {
					for _, tc := range tracked {
						if tc.EveCorpID == cid {
							receiverCorpID = cid
							break
						}
					}
				}
			}
		}

		if receiverCorpID <= 0 {
			if len(tracked) == 1 {
				receiverCorpID = tracked[0].EveCorpID
			} else {
				return 0, 0, "", fmt.Errorf("multiple corporations are tracked for this server; please select the target corporation")
			}
		}
	}

	var targetRoleID string
	if tracked, err := db.GetTrackedCorporationsForGuild(guildID); err == nil {
		for _, tc := range tracked {
			if tc.EveCorpID == receiverCorpID {
				targetRoleID = tc.RoleID
				break
			}
		}
	}
	corpName := GetTrackedCorpDisplayName(guildID, targetRoleID, receiverCorpID)

	var donorNames []string
	for _, e := range entries {
		if e.DonorName != "" {
			donorNames = append(donorNames, e.DonorName)
		}
	}

	nameToID, nameToType, _ := ResolveUniverseNames(donorNames)

	importedCount := 0
	duplicateCount := 0

	for _, entry := range entries {
		if entry.Amount <= 0 {
			continue
		}

		lowerName := strings.ToLower(entry.DonorName)
		donorID, ok := nameToID[lowerName]
		donorType := nameToType[lowerName]
		if !ok || donorID == 0 {
			// Fallback: deterministic hash from name for test/offline resilience
			h := fnv.New32a()
			h.Write([]byte(lowerName))
			donorID = int(h.Sum32() & 0x7FFFFFFF)
			donorType = "character"
			SetCachedEntity(donorID, entry.DonorName, donorType)
		}

		exists, err := db.DonationExists(receiverCorpID, donorID, entry.Amount, entry.Balance, entry.Date)
		if err == nil && exists {
			duplicateCount++
			continue
		}

		h := fnv.New64a()
		h.Write([]byte(fmt.Sprintf("%d:%d:%.2f:%.2f:%s", receiverCorpID, donorID, entry.Amount, entry.Balance, entry.Date.UTC().Format("2006-01-02 15:04"))))
		txID := int64(h.Sum64() & 0x7FFFFFFFFFFFFFFF)

		rec := db.DonationRecord{
			TransactionID:  txID,
			ReceiverCorpID: receiverCorpID,
			DonorID:        donorID,
			DonorType:      donorType,
			Amount:         entry.Amount,
			Balance:        entry.Balance,
			Date:           entry.Date,
		}

		err = db.InsertDonationRecord(rec)
		if err != nil {
			log.Printf("[Donations Import] Error inserting record for %s (amount: %f): %v", entry.DonorName, entry.Amount, err)
			continue
		}
		importedCount++
	}

	return importedCount, duplicateCount, corpName, nil
}
