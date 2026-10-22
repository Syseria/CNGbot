package fleet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"evemaildiscord/config"
	"evemaildiscord/db"
	"evemaildiscord/esi"
	"evemaildiscord/seat"

	"github.com/bwmarrin/discordgo"
)

var (
	esiBase = "https://esi.evetech.net/latest" // var (not const) so tests can point at httptest
	// var (not const) so tests can point at httptest
	affiliationURL = "https://esi.evetech.net/latest/characters/affiliation/"
)

const (
	pollInterval   = 60 * time.Second
	inviteCooldown = 10 * time.Minute
	warnInterval   = 10 * time.Minute
)

// isJSpace reports whether a solar system ID is wormhole space (J-space).
func isJSpace(systemID int) bool {
	return systemID >= 31000000 && systemID < 32000000
}

// ownFleet mirrors GET /characters/{id}/fleet/
type ownFleet struct {
	FleetID int64  `json:"fleet_id"`
	Role    string `json:"role"`
}

// fleetMember mirrors one entry of GET /fleets/{id}/members/
type fleetMember struct {
	CharacterID   int    `json:"character_id"`
	Role          string `json:"role"`
	SolarSystemID int    `json:"solar_system_id"`
}

var warnMu sync.Mutex
var lastWarn = make(map[string]int64) // "guildID|msg" -> unix

// PollAutoInvite runs the auto-invite check every pollInterval, starting immediately.
func PollAutoInvite(dg *discordgo.Session) {
	CheckAutoInvite(dg)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for range ticker.C {
		CheckAutoInvite(dg)
	}
}

// CheckAutoInvite processes every guild the bot is in.
func CheckAutoInvite(dg *discordgo.Session) {
	for _, guild := range dg.State.Guilds {
		checkGuild(dg, guild.ID)
	}
}

// getFleetAlliance returns the alliance ID configured for auto fleet invite,
// or 0 when none is set.
func getFleetAlliance(guildID string) int {
	var val string
	if err := db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'fleet_alliance_id'", guildID).Scan(&val); err != nil {
		return 0
	}
	id, err := strconv.Atoi(val)
	if err != nil {
		return 0
	}
	return id
}

// filterByAlliance returns the subset of character IDs that ESI affiliation
// reports as members of the given alliance. ESI accepts up to 1000 IDs per call.
func filterByAlliance(charIDs map[int]bool, allianceID int) (map[int]bool, error) {
	ids := make([]int, 0, len(charIDs))
	for id := range charIDs {
		ids = append(ids, id)
	}
	bodyBytes, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("failed to encode character IDs for affiliation: %w", err)
	}
	resp, err := esi.Post(affiliationURL, "application/json", bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to request character affiliations: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("ESI character affiliation returned status %d", resp.StatusCode)
	}
	var affiliations []struct {
		CharacterID int `json:"character_id"`
		AllianceID  int `json:"alliance_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&affiliations); err != nil {
		return nil, fmt.Errorf("error decoding character affiliations: %w", err)
	}
	result := make(map[int]bool)
	for _, aff := range affiliations {
		if aff.AllianceID == allianceID {
			result[aff.CharacterID] = true
		}
	}
	return result, nil
}

// bossCandidate is a linked character that is currently the fleet boss of a
// fleet whose boss is in wormhole space.
type bossCandidate struct {
	client      *http.Client
	charID      int
	fleetID     int64
	memberCount int
	members     map[int]fleetMember
}

// bossCandidateFor checks one character: it must be in a fleet as fleet
// commander and the fleet roster must place it in wormhole space.
func bossCandidateFor(dg *discordgo.Session, guildID string, client *http.Client, charID int) (bossCandidate, bool) {
	fleetID, role, status, err := getOwnFleet(client, charID)
	if err != nil {
		log.Printf("[Fleet] Guild %s: failed to resolve fleet for character %d: %v", guildID, charID, err)
		if status == http.StatusForbidden {
			warnThrottled(dg, guildID, fmt.Sprintf("Auto fleet invite: the ESI token for character %d is missing the esi-fleets scopes. Re-link it via the dashboard (Link EVE Character) to authorize them.", charID))
		}
		return bossCandidate{}, false
	}
	if fleetID == 0 {
		return bossCandidate{}, false // not in a fleet
	}
	members, err := getFleetMembers(client, fleetID)
	if err != nil {
		log.Printf("[Fleet] Guild %s: failed to fetch fleet %d members: %v", guildID, fleetID, err)
		return bossCandidate{}, false
	}
	if role != "fleet_commander" {
		return bossCandidate{}, false // in a fleet but cannot invite
	}
	boss, ok := members[charID]
	if !ok || !isJSpace(boss.SolarSystemID) {
		return bossCandidate{}, false // boss not in wormhole space
	}
	return bossCandidate{client: client, charID: charID, fleetID: fleetID, memberCount: len(members), members: members}, true
}

// findBossCandidates checks every character linked for this guild (personal
// dashboard links first, then the legacy guild/env character) and returns the
// ones that are fleet bosses of a wormhole-space fleet.
func findBossCandidates(dg *discordgo.Session, guildID string, allianceChars map[int]bool) []bossCandidate {
	var candidates []bossCandidate

	tokens, err := db.GetGuildCharacterTokens(guildID)
	if err != nil {
		log.Printf("[Fleet] Guild %s: failed to load linked characters: %v", guildID, err)
	}
	for charID, token := range tokens {
		if !allianceChars[charID] {
			continue
		}
		client := esi.GetOAuthHTTPClientForToken(token)
		if client == nil {
			continue
		}
		if c, ok := bossCandidateFor(dg, guildID, client, charID); ok {
			candidates = append(candidates, c)
		}
	}
	if len(candidates) > 0 {
		return candidates
	}

	// Legacy fallback: the guild-linked dashboard character, then the env token.
	integ, err := db.GetGuildIntegration(guildID)
	charID := 0
	if err == nil {
		charID = integ.EveCharacterID
	}
	if charID <= 0 {
		if envCharID := config.GetCharacterID(); envCharID != "" {
			charID, _ = strconv.Atoi(envCharID)
		}
	}
	legacyLinked := charID > 0
	if legacyLinked {
		if client := esi.GetOAuthHTTPClient(guildID); client != nil {
			if c, ok := bossCandidateFor(dg, guildID, client, charID); ok {
				candidates = append(candidates, c)
			}
		}
	}
	if len(tokens) == 0 && !legacyLinked {
		warnThrottled(dg, guildID, "Auto fleet invite is enabled but no EVE character has been linked for it. Fleet bosses can link their personal character via the dashboard (Link EVE Character).")
	}
	return candidates
}

func checkGuild(dg *discordgo.Session, guildID string) {
	if !isEnabled(guildID) {
		return
	}
	allianceID := getFleetAlliance(guildID)
	if allianceID <= 0 {
		warnThrottled(dg, guildID, "Auto fleet invite is enabled but no alliance is configured. Use /set_fleet_alliance first.")
		return
	}

	seatChars, err := seat.GetValidSeatCharacters(guildID)
	if err != nil {
		log.Printf("[Fleet] Guild %s: failed to fetch SeAT characters: %v", guildID, err)
		return
	}
	allianceChars, err := filterByAlliance(seatChars, allianceID)
	if err != nil {
		log.Printf("[Fleet] Guild %s: failed to fetch character affiliations: %v", guildID, err)
		return
	}
	if len(allianceChars) == 0 {
		return
	}

	baseURL, apiKey, err := seat.GetSeatConfig(guildID)
	if err != nil {
		log.Printf("[Fleet] Guild %s: failed to resolve SeAT config: %v", guildID, err)
		return
	}

	candidates := findBossCandidates(dg, guildID, allianceChars)
	if len(candidates) == 0 {
		return // no linked character is currently a fleet boss in wormhole space
	}

	// Multiple fleets can be up during ops; serve the largest one.
	pick := candidates[0]
	for _, c := range candidates[1:] {
		if c.memberCount > pick.memberCount {
			pick = c
		}
	}

	now := time.Now().Unix()
	cooldown := int64(inviteCooldown / time.Second)
	var invited []int
	for charID := range allianceChars {
		if charID == pick.charID {
			continue
		}
		if _, inFleet := pick.members[charID]; inFleet {
			continue
		}
		if last, err := db.GetFleetLastInvite(charID); err == nil && now-last < cooldown {
			continue
		}
		sysID, err := seat.FetchCharacterSolarSystem(baseURL, apiKey, charID)
		if err != nil {
			log.Printf("[Fleet] Guild %s: failed to fetch location for character %d: %v", guildID, charID, err)
			continue
		}
		if !isJSpace(sysID) {
			continue
		}
		// Record the attempt regardless of outcome so declined invites and
		// offline/already-in-another-fleet characters are not hammered.
		_ = db.SetFleetInvite(charID, now)
		if err := sendInvite(pick.client, pick.fleetID, charID); err != nil {
			log.Printf("[Fleet] Guild %s: invite for character %d failed: %v", guildID, charID, err)
			continue
		}
		invited = append(invited, charID)
	}

	if len(invited) > 0 {
		db.DiscordLog(dg, guildID, fmt.Sprintf("Auto fleet invite: sent %d invite(s) into fleet %d for alliance %d members (character IDs: %v).", len(invited), pick.fleetID, allianceID, invited))
	}
}

func isEnabled(guildID string) bool {
	var val string
	err := db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'auto_fleet_invite_enabled'", guildID).Scan(&val)
	return err == nil && val == "1"
}

// warnThrottled posts an operational warning to the guild log channel at most
// once per warnInterval for the same message.
func warnThrottled(dg *discordgo.Session, guildID, msg string) {
	key := guildID + "|" + msg
	now := time.Now().Unix()
	warnMu.Lock()
	if now-lastWarn[key] < int64(warnInterval/time.Second) {
		warnMu.Unlock()
		return
	}
	lastWarn[key] = now
	warnMu.Unlock()
	db.DiscordLog(dg, guildID, msg)
}

// getOwnFleet returns the fleet the character is in, or (0, "", 404, nil) when
// it is not in a fleet (ESI returns 404 in that case).
func getOwnFleet(client *http.Client, charID int) (int64, string, int, error) {
	resp, err := client.Get(fmt.Sprintf("%s/characters/%d/fleet/", esiBase, charID))
	if err != nil {
		return 0, "", 0, fmt.Errorf("failed to request ESI character fleet: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return 0, "", resp.StatusCode, nil
	}
	if resp.StatusCode != 200 {
		return 0, "", resp.StatusCode, fmt.Errorf("ESI characters fleet returned status %d", resp.StatusCode)
	}

	var fleet ownFleet
	if err := json.NewDecoder(resp.Body).Decode(&fleet); err != nil {
		return 0, "", resp.StatusCode, fmt.Errorf("error decoding ESI character fleet: %w", err)
	}
	return fleet.FleetID, fleet.Role, resp.StatusCode, nil
}

// getFleetMembers returns the fleet roster keyed by character ID.
func getFleetMembers(client *http.Client, fleetID int64) (map[int]fleetMember, error) {
	resp, err := client.Get(fmt.Sprintf("%s/fleets/%d/members/", esiBase, fleetID))
	if err != nil {
		return nil, fmt.Errorf("failed to request ESI fleet members: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("ESI fleet members returned status %d", resp.StatusCode)
	}

	var list []fleetMember
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("error decoding ESI fleet members: %w", err)
	}
	members := make(map[int]fleetMember, len(list))
	for _, m := range list {
		members[m.CharacterID] = m
	}
	return members, nil
}

// sendInvite invites a character into the fleet as a regular fleet member.
func sendInvite(client *http.Client, fleetID int64, charID int) error {
	body, err := json.Marshal(map[string]interface{}{
		"character_id": charID,
		"role":         "fleet_member",
	})
	if err != nil {
		return fmt.Errorf("failed to marshal fleet invite request: %w", err)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/fleets/%d/members/", esiBase, fleetID), bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to create fleet invite request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send fleet invite via ESI: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("ESI fleet invite returned status %d", resp.StatusCode)
	}
	return nil
}
