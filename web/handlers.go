package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"evemaildiscord/config"
	"evemaildiscord/db"
	"evemaildiscord/donations"
	"evemaildiscord/esi"
	"evemaildiscord/wanderer"

	"github.com/bwmarrin/discordgo"
)

type WebHandler struct {
	dg *discordgo.Session
}

func NewWebHandler(dg *discordgo.Session) *WebHandler {
	return &WebHandler{dg: dg}
}

func (h *WebHandler) HandleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	session := Store.GetSession(r)
	if session != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	_ = RenderLanding(w)
}

func (h *WebHandler) HandleDiscordLogin(w http.ResponseWriter, r *http.Request) {
	clientID := config.GetDiscordClientID()
	if clientID == "" {
		http.Error(w, "DISCORD_CLIENT_ID is not configured", http.StatusInternalServerError)
		return
	}

	state, err := GenerateRandomToken(16)
	if err != nil {
		http.Error(w, "Failed to generate security state", http.StatusInternalServerError)
		return
	}

	Store.SaveOAuthState(state, "", "", "discord", "")

	redirectURI := config.GetDiscordOAuthRedirect()
	authURL := fmt.Sprintf(
		"https://discord.com/api/oauth2/authorize?client_id=%s&redirect_uri=%s&response_type=code&scope=identify%%20guilds&state=%s",
		url.QueryEscape(clientID),
		url.QueryEscape(redirectURI),
		url.QueryEscape(state),
	)

	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

type discordTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

type discordUser struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	Discriminator string `json:"discriminator"`
	Avatar        string `json:"avatar"`
}

type discordUserGuild struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	Owner       bool   `json:"owner"`
	Permissions string `json:"permissions"`
}

var discordAPIBase = "https://discord.com/api/v10"

// esiAffiliationURL is the public ESI endpoint used to resolve a character's
// alliance (var so tests can point it at a stub).
var esiAffiliationURL = "https://esi.evetech.net/latest/characters/affiliation/"

// fetchUserGuilds returns the Discord guilds the session user is a member of.
func fetchUserGuilds(accessToken string) ([]discordUserGuild, error) {
	req, err := http.NewRequest("GET", discordAPIBase+"/users/@me/guilds", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discord guilds endpoint returned status %d", resp.StatusCode)
	}

	var guilds []discordUserGuild
	if err := json.NewDecoder(resp.Body).Decode(&guilds); err != nil {
		return nil, err
	}
	return guilds, nil
}

func (h *WebHandler) HandleDiscordCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")

	if state == "" || code == "" {
		http.Error(w, "Missing state or authorization code", http.StatusBadRequest)
		return
	}

	_, valid := Store.ValidateAndConsumeOAuthState(state, "discord")
	if !valid {
		http.Error(w, "Invalid or expired state parameter", http.StatusBadRequest)
		return
	}

	clientID := config.GetDiscordClientID()
	clientSecret := config.GetDiscordClientSecret()
	redirectURI := config.GetDiscordOAuthRedirect()

	if clientID == "" || clientSecret == "" {
		http.Error(w, "Discord OAuth credentials not configured", http.StatusInternalServerError)
		return
	}

	form := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	}

	resp, err := http.PostForm("https://discord.com/api/oauth2/token", form)
	if err != nil {
		log.Printf("Error exchanging Discord code: %v", err)
		http.Error(w, "Failed to exchange Discord authorization code", http.StatusInternalServerError)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Discord token endpoint returned status %d: %s", resp.StatusCode, string(body))
		http.Error(w, "Discord token exchange failed", http.StatusBadRequest)
		return
	}

	var tokenResp discordTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		http.Error(w, "Failed to decode Discord token", http.StatusInternalServerError)
		return
	}

	// Fetch user info
	userReq, _ := http.NewRequest("GET", "https://discord.com/api/v10/users/@me", nil)
	userReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)
	userResp, err := http.DefaultClient.Do(userReq)
	if err != nil || userResp.StatusCode != http.StatusOK {
		if userResp != nil {
			_ = userResp.Body.Close()
		}
		http.Error(w, "Failed to fetch user profile from Discord", http.StatusInternalServerError)
		return
	}

	var dUser discordUser
	_ = json.NewDecoder(userResp.Body).Decode(&dUser)
	_ = userResp.Body.Close()

	// Fetch user guilds
	guildsReq, _ := http.NewRequest("GET", "https://discord.com/api/v10/users/@me/guilds", nil)
	guildsReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)
	guildsResp, err := http.DefaultClient.Do(guildsReq)
	if err != nil || guildsResp.StatusCode != http.StatusOK {
		if guildsResp != nil {
			_ = guildsResp.Body.Close()
		}
		http.Error(w, "Failed to fetch guilds from Discord", http.StatusInternalServerError)
		return
	}

	var rawGuilds []discordUserGuild
	_ = json.NewDecoder(guildsResp.Body).Decode(&rawGuilds)
	_ = guildsResp.Body.Close()

	adminGuilds := make(map[string]DiscordGuildInfo)
	for _, g := range rawGuilds {
		permsInt, _ := strconv.ParseInt(g.Permissions, 10, 64)
		// Administrator permission flag is 0x8
		isAdmin := (permsInt&0x8) != 0 || g.Owner
		if isAdmin {
			botInGuild := false
			if h.dg != nil && h.dg.State != nil {
				if _, err := h.dg.State.Guild(g.ID); err == nil {
					botInGuild = true
				}
			}
			adminGuilds[g.ID] = DiscordGuildInfo{
				ID:          g.ID,
				Name:        g.Name,
				Icon:        g.Icon,
				Owner:       g.Owner,
				Permissions: g.Permissions,
				HasAdmin:    true,
				BotInGuild:  botInGuild,
			}
		}
	}

	session, err := Store.CreateSession(dUser.ID, dUser.Username, dUser.Avatar, tokenResp.AccessToken, adminGuilds)
	if err != nil {
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	Store.SetSessionCookie(w, session.ID, session.ExpiresAt)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (h *WebHandler) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	session := Store.GetSession(r)
	if session == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	var guildsList []GuildCardView
	for _, g := range session.AdminGuilds {
		if h.dg != nil && h.dg.State != nil {
			if _, err := h.dg.State.Guild(g.ID); err == nil {
				g.BotInGuild = true
			} else {
				g.BotInGuild = false
			}
		}

		integ, _ := db.GetGuildIntegration(g.ID)
		eveLinked := integ.EveCharacterID > 0 && integ.EveRefreshToken != ""
		seatConfigured := integ.SeatURL != "" && integ.SeatAPIKey != ""

		guildsList = append(guildsList, GuildCardView{
			DiscordGuildInfo: g,
			EveLinked:        eveLinked,
			SeatConfigured:   seatConfigured,
		})
	}

	var memberGuilds []DiscordGuildInfo
	userGuilds, err := fetchUserGuilds(session.AccessToken)
	if err != nil {
		log.Printf("Failed to fetch Discord guilds for user %s: %v", session.UserID, err)
	}
	for _, g := range userGuilds {
		if _, isAdmin := session.AdminGuilds[g.ID]; isAdmin {
			continue
		}
		if h.dg == nil || h.dg.State == nil {
			continue
		}
		if _, err := h.dg.State.Guild(g.ID); err != nil {
			continue
		}
		memberGuilds = append(memberGuilds, DiscordGuildInfo{
			ID:         g.ID,
			Name:       g.Name,
			Icon:       g.Icon,
			HasAdmin:   false,
			BotInGuild: true,
		})
	}

	botClientID := config.GetDiscordClientID()
	botInviteURL := fmt.Sprintf(
		"https://discord.com/api/oauth2/authorize?client_id=%s&permissions=268435456&scope=bot%%20applications.commands",
		url.QueryEscape(botClientID),
	)

	_ = RenderDashboard(w, DashboardData{
		User:         session,
		Guilds:       guildsList,
		MemberGuilds: memberGuilds,
		BotInviteURL: botInviteURL,
	})
}

func (h *WebHandler) HandleGuildConfig(w http.ResponseWriter, r *http.Request) {
	session := Store.GetSession(r)
	if session == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	guildID := r.URL.Query().Get("id")
	if guildID == "" {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	guildInfo, hasAccess := session.AdminGuilds[guildID]
	if !hasAccess {
		http.Error(w, "You do not have Administrator access to this Discord server", http.StatusForbidden)
		return
	}

	integ, _ := db.GetGuildIntegration(guildID)

	var eveCharName string
	if integ.EveCharacterID > 0 {
		eveCharName = fetchCharacterName(integ.EveCharacterID)
	}

	var confData struct {
		MailChannel   string
		LogChannel    string
		EventsChannel string
		GuestRole     string
		TwoFA         string
		AutoMap       string
		SyncRoles     string
		WandererDSN   string
	}

	if db.DB != nil {
		_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND (key = 'mail_channel' OR key = 'evemail_channel')", guildID).Scan(&confData.MailChannel)
		_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'log_channel'", guildID).Scan(&confData.LogChannel)
		_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'events_channel'", guildID).Scan(&confData.EventsChannel)
		_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'guest_role'", guildID).Scan(&confData.GuestRole)
		_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'enable_2fa'", guildID).Scan(&confData.TwoFA)
		_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'auto_map_chars'", guildID).Scan(&confData.AutoMap)
		_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'sync_roles'", guildID).Scan(&confData.SyncRoles)
		_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'wanderer_dsn'", guildID).Scan(&confData.WandererDSN)
	}

	var trackedCorps []TrackedCorpView
	if tcs, err := db.GetTrackedCorporationsForGuild(guildID); err == nil {
		for _, tc := range tcs {
			name := donations.GetTrackedCorpDisplayName(guildID, tc.RoleID, tc.EveCorpID, h.dg)
			trackedCorps = append(trackedCorps, TrackedCorpView{
				RoleID:    tc.RoleID,
				EveCorpID: tc.EveCorpID,
				CorpName:  name,
			})
		}
	}

	successCode := r.URL.Query().Get("success")
	errorCode := r.URL.Query().Get("error")

	_ = RenderGuildConfig(w, GuildConfigData{
		User:             session,
		Guild:            guildInfo,
		Integration:      integ,
		EveCharacterName: eveCharName,
		TrackedCorps:     trackedCorps,
		Config:           confData,
		Success:          successCode,
		Error:            errorCode,
	})
}

func (h *WebHandler) HandleEveLogin(w http.ResponseWriter, r *http.Request) {
	guildID := r.URL.Query().Get("guild_id")
	if guildID == "" {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	mode := r.URL.Query().Get("mode")
	if mode != "character" {
		mode = ""
	}

	// character mode (the /link flow) is EVE-only: no Discord login required,
	// the server-side OAuth state binds the request to the guild.
	stateSessionID := ""
	if mode != "character" {
		session := Store.GetSession(r)
		if session == nil {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		if _, ok := session.AdminGuilds[guildID]; !ok {
			http.Error(w, "You do not have Administrator permissions for this server", http.StatusForbidden)
			return
		}
		stateSessionID = session.ID
	}

	clientID := config.GetEveClientID()
	if clientID == "" {
		http.Error(w, "EVE_CLIENT_ID is not configured", http.StatusInternalServerError)
		return
	}

	state, err := GenerateRandomToken(16)
	if err != nil {
		http.Error(w, "Failed to generate security state", http.StatusInternalServerError)
		return
	}

	Store.SaveOAuthState(state, stateSessionID, guildID, "eve", mode)

	scopes := []string{
		"esi-mail.read_mail.v1",
		"esi-mail.send_mail.v1",
		"esi-characters.read_contacts.v1",
		"esi-alliances.read_contacts.v1",
		"esi-corporations.read_contacts.v1",
		"esi-corporations.read_standings.v1",
		"esi-wallet.read_corporation_wallets.v1",
		"esi-fleets.read_fleet.v1",
		"esi-fleets.write_fleet.v1",
		"esi-location.read_location.v1",
		"publicData",
	}
	if mode == "character" {
		// The /link flow only powers auto fleet invite: fleet + location scopes,
		// none of the admin mail/contacts/standings/wallet scopes.
		scopes = []string{
			"publicData",
			"esi-fleets.read_fleet.v1",
			"esi-fleets.write_fleet.v1",
			"esi-location.read_location.v1",
		}
	}

	callbackURL := config.GetEveCallbackURL()
	authURL := fmt.Sprintf(
		"https://login.eveonline.com/v2/oauth/authorize?response_type=code&redirect_uri=%s&client_id=%s&scope=%s&state=%s",
		url.QueryEscape(callbackURL),
		url.QueryEscape(clientID),
		url.QueryEscape(strings.Join(scopes, " ")),
		url.QueryEscape(state),
	)

	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

type eveTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

type eveVerifyResponse struct {
	CharacterID   int    `json:"CharacterID"`
	CharacterName string `json:"CharacterName"`
}

// fleetAllianceForGuild returns the alliance configured for auto fleet invite
// for the guild, or 0 when none is set.
func fleetAllianceForGuild(guildID string) int {
	if db.DB == nil {
		return 0
	}
	var val string
	if err := db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'fleet_alliance_id'", guildID).Scan(&val); err != nil {
		return 0
	}
	id, _ := strconv.Atoi(val)
	return id
}

// characterInAlliance reports whether the character's ESI affiliation lists
// the given alliance. Characters missing from the affiliation response
// (unknown or too new) count as not in the alliance.
func characterInAlliance(charID, allianceID int) (bool, error) {
	bodyBytes, err := json.Marshal([]int{charID})
	if err != nil {
		return false, fmt.Errorf("failed to encode character ID for affiliation: %w", err)
	}
	resp, err := esi.Post(esiAffiliationURL, "application/json", bytes.NewBuffer(bodyBytes))
	if err != nil {
		return false, fmt.Errorf("failed to request character affiliation: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return false, fmt.Errorf("ESI character affiliation returned status %d", resp.StatusCode)
	}
	var affiliations []struct {
		CharacterID int `json:"character_id"`
		AllianceID  int `json:"alliance_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&affiliations); err != nil {
		return false, fmt.Errorf("error decoding character affiliation: %w", err)
	}
	for _, aff := range affiliations {
		if aff.CharacterID == charID {
			return aff.AllianceID == allianceID, nil
		}
	}
	return false, nil
}

func (h *WebHandler) HandleEveCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")

	if state == "" || code == "" {
		http.Error(w, "Missing state or authorization code", http.StatusBadRequest)
		return
	}

	stateData, valid := Store.ValidateAndConsumeOAuthState(state, "eve")
	if !valid {
		http.Error(w, "Invalid or expired state parameter", http.StatusBadRequest)
		return
	}

	// character mode (the /link flow) has no Discord session; the server-side
	// state binds the guild. The guild mode is session-bound and admin-gated.
	linkedBy := ""
	if stateData.Mode != "character" {
		session := Store.GetSession(r)
		if session == nil || stateData.SessionID != session.ID {
			http.Error(w, "Invalid or expired state parameter", http.StatusBadRequest)
			return
		}
		if _, ok := session.AdminGuilds[stateData.GuildID]; !ok {
			http.Error(w, "You do not have Administrator permissions for this server", http.StatusForbidden)
			return
		}
		linkedBy = session.UserID
	}

	guildID := stateData.GuildID

	clientID := config.GetEveClientID()
	clientSecret := config.GetEveSecret()
	if clientID == "" || clientSecret == "" {
		http.Error(w, "EVE SSO credentials not configured", http.StatusInternalServerError)
		return
	}

	form := url.Values{
		"grant_type": {"authorization_code"},
		"code":       {code},
	}

	req, err := http.NewRequest("POST", "https://login.eveonline.com/v2/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		http.Error(w, "Failed to create EVE token request", http.StatusInternalServerError)
		return
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	basicAuth := base64.StdEncoding.EncodeToString([]byte(clientID + ":" + clientSecret))
	req.Header.Set("Authorization", "Basic "+basicAuth)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("Error exchanging EVE SSO code: %v", err)
		http.Error(w, "Failed to exchange EVE SSO code", http.StatusInternalServerError)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("EVE SSO token endpoint returned %d: %s", resp.StatusCode, string(body))
		http.Error(w, "EVE SSO token exchange failed", http.StatusBadRequest)
		return
	}

	var tokenResp eveTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		http.Error(w, "Failed to decode EVE SSO token", http.StatusInternalServerError)
		return
	}

	charID, charName := extractEveCharacterFromToken(tokenResp.AccessToken)
	if charID <= 0 {
		http.Error(w, "Failed to identify EVE Character from token", http.StatusInternalServerError)
		return
	}

	if stateData.Mode == "character" {
		allianceID := fleetAllianceForGuild(guildID)
		if allianceID <= 0 {
			http.Error(w, "This server has no alliance configured for auto fleet invite yet (/set_fleet_alliance).", http.StatusForbidden)
			return
		}
		inAlliance, err := characterInAlliance(charID, allianceID)
		if err != nil {
			log.Printf("Failed to verify alliance for character %d (guild %s): %v", charID, guildID, err)
			http.Error(w, "Failed to verify the character's alliance", http.StatusInternalServerError)
			return
		}
		if !inAlliance {
			log.Printf("Rejected character %d link for guild %s: not in alliance %d", charID, guildID, allianceID)
			http.Error(w, fmt.Sprintf("Character %s is not in the alliance configured for this server, so it cannot be linked for fleet invites.", html.EscapeString(charName)), http.StatusForbidden)
			return
		}

		err = db.SetCharacterToken(charID, tokenResp.RefreshToken, guildID, "")
		if err != nil {
			log.Printf("Failed to save EVE character token for character %d (guild %s): %v", charID, guildID, err)
			http.Error(w, "Failed to save EVE authentication", http.StatusInternalServerError)
			return
		}

		if h.dg != nil {
			db.DiscordLog(h.dg, guildID, fmt.Sprintf("EVE Online Character **%s** (ID: %d) linked for auto fleet invite.", charName, charID))
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<html><body><h2>EVE character %s linked.</h2><p>You can close this page. The bot will use this character for auto fleet invite while it is your fleet's boss.</p></body></html>", html.EscapeString(charName))
		return
	}

	err = db.SetGuildEveAuth(guildID, charID, tokenResp.RefreshToken)
	if err != nil {
		log.Printf("Failed to save EVE SSO auth for guild %s: %v", guildID, err)
		http.Error(w, "Failed to save EVE authentication", http.StatusInternalServerError)
		return
	}

	if h.dg != nil {
		db.DiscordLog(h.dg, guildID, fmt.Sprintf("EVE Online Character **%s** (ID: %d) successfully linked to this server by <@%s>.", charName, charID, linkedBy))
	}

	http.Redirect(w, r, "/dashboard/guild?id="+guildID+"&success=eve_linked", http.StatusSeeOther)
}

func (h *WebHandler) HandleGuildSeatSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session := Store.GetSession(r)
	if session == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	_ = r.ParseForm()
	guildID := r.FormValue("guild_id")
	seatURL := strings.TrimSpace(r.FormValue("seat_url"))
	seatAPIKey := strings.TrimSpace(r.FormValue("seat_api_key"))

	if guildID == "" {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	if _, ok := session.AdminGuilds[guildID]; !ok {
		http.Error(w, "You do not have Administrator permissions for this server", http.StatusForbidden)
		return
	}

	seatURL = strings.TrimRight(seatURL, "/")

	err := db.SetGuildSeatConfig(guildID, seatURL, seatAPIKey)
	if err != nil {
		log.Printf("Error saving SeAT config for guild %s: %v", guildID, err)
		http.Redirect(w, r, "/dashboard/guild?id="+guildID+"&error=Failed+to+save+SeAT+settings", http.StatusSeeOther)
		return
	}

	if h.dg != nil {
		db.DiscordLog(h.dg, guildID, fmt.Sprintf("SeAT API configuration updated by <@%s>.", session.UserID))
	}

	http.Redirect(w, r, "/dashboard/guild?id="+guildID+"&success=seat_saved", http.StatusSeeOther)
}

func (h *WebHandler) HandleGuildWandererSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session := Store.GetSession(r)
	if session == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	_ = r.ParseForm()
	guildID := r.FormValue("guild_id")
	if guildID == "" {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	if _, ok := session.AdminGuilds[guildID]; !ok {
		http.Error(w, "You do not have Administrator permissions for this server", http.StatusForbidden)
		return
	}

	dsn := strings.TrimSpace(r.FormValue("wanderer_dsn"))
	if dsn != "" {
		if err := wanderer.ValidateDSN(dsn); err != nil {
			log.Printf("wanderer DSN validation failed for guild %s: %v", guildID, err)
			http.Redirect(w, r, "/dashboard/guild?id="+guildID+"&error=Could+not+connect+to+the+wanderer+database+with+that+connection+string", http.StatusSeeOther)
			return
		}
	}

	if err := db.SetGuildWandererDSN(guildID, dsn); err != nil {
		log.Printf("Error saving wanderer config for guild %s: %v", guildID, err)
		http.Redirect(w, r, "/dashboard/guild?id="+guildID+"&error=Failed+to+save+wanderer+settings", http.StatusSeeOther)
		return
	}

	if h.dg != nil {
		db.DiscordLog(h.dg, guildID, fmt.Sprintf("Wanderer database configuration updated by <@%s>.", session.UserID))
	}

	http.Redirect(w, r, "/dashboard/guild?id="+guildID+"&success=wanderer_saved", http.StatusSeeOther)
}

func (h *WebHandler) HandleGuildAutomationSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session := Store.GetSession(r)
	if session == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	_ = r.ParseForm()
	guildID := r.FormValue("guild_id")
	if guildID == "" {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	if _, ok := session.AdminGuilds[guildID]; !ok {
		http.Error(w, "You do not have Administrator permissions for this server", http.StatusForbidden)
		return
	}

	autoMap := r.FormValue("auto_map_chars")
	syncRoles := r.FormValue("sync_roles")

	if db.DB != nil {
		_, _ = db.DB.Exec("INSERT OR REPLACE INTO config (guild_id, key, value) VALUES (?, 'auto_map_chars', ?)", guildID, autoMap)
		_, _ = db.DB.Exec("INSERT OR REPLACE INTO config (guild_id, key, value) VALUES (?, 'sync_roles', ?)", guildID, syncRoles)
	}

	db.DiscordLog(h.dg, guildID, fmt.Sprintf("Automation settings updated by <@%s>.", session.UserID))
	http.Redirect(w, r, "/dashboard/guild?id="+guildID+"&success=1", http.StatusSeeOther)
}

func (h *WebHandler) HandleGuildEveUnlink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session := Store.GetSession(r)
	if session == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	_ = r.ParseForm()
	guildID := r.FormValue("guild_id")

	if guildID == "" {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	if _, ok := session.AdminGuilds[guildID]; !ok {
		http.Error(w, "You do not have Administrator permissions for this server", http.StatusForbidden)
		return
	}

	err := db.DeleteGuildEveAuth(guildID)
	if err != nil {
		log.Printf("Error unlinking EVE auth for guild %s: %v", guildID, err)
		http.Redirect(w, r, "/dashboard/guild?id="+guildID+"&error=Failed+to+unlink+EVE+character", http.StatusSeeOther)
		return
	}

	if h.dg != nil {
		db.DiscordLog(h.dg, guildID, fmt.Sprintf("EVE character integration unlinked from this server by <@%s>.", session.UserID))
	}

	http.Redirect(w, r, "/dashboard/guild?id="+guildID+"&success=eve_unlinked", http.StatusSeeOther)
}

func (h *WebHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	Store.DeleteSession(w, r)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func extractEveCharacterFromToken(accessToken string) (int, string) {
	// 1. Try verify endpoint first
	verifyReq, err := http.NewRequest("GET", "https://login.eveonline.com/oauth/verify", nil)
	if err == nil {
		verifyReq.Header.Set("Authorization", "Bearer "+accessToken)
		verifyResp, errDo := http.DefaultClient.Do(verifyReq)
		if errDo == nil && verifyResp.StatusCode == http.StatusOK {
			var vResp eveVerifyResponse
			if json.NewDecoder(verifyResp.Body).Decode(&vResp) == nil && vResp.CharacterID > 0 {
				_ = verifyResp.Body.Close()
				return vResp.CharacterID, vResp.CharacterName
			}
			_ = verifyResp.Body.Close()
		} else if verifyResp != nil {
			_ = verifyResp.Body.Close()
		}
	}

	// 2. Fallback: Parse JWT payload (sub: "CHARACTER:EVE:<id>", name: "<name>")
	parts := strings.Split(accessToken, ".")
	if len(parts) >= 2 {
		payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err == nil {
			var claims struct {
				Sub  string `json:"sub"`
				Name string `json:"name"`
			}
			if json.NewDecoder(bytes.NewReader(payloadBytes)).Decode(&claims) == nil {
				if strings.HasPrefix(claims.Sub, "CHARACTER:EVE:") {
					idStr := strings.TrimPrefix(claims.Sub, "CHARACTER:EVE:")
					if id, errID := strconv.Atoi(idStr); errID == nil && id > 0 {
						return id, claims.Name
					}
				}
			}
		}
	}

	return 0, ""
}

func fetchCharacterName(charID int) string {
	resp, err := http.Get(fmt.Sprintf("https://esi.evetech.net/latest/characters/%d/", charID))
	if err != nil {
		return fmt.Sprintf("Character ID: %d", charID)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("Character ID: %d", charID)
	}

	var data struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil || data.Name == "" {
		return fmt.Sprintf("Character ID: %d", charID)
	}

	return data.Name
}

func (h *WebHandler) HandleGuildDonationsImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session := Store.GetSession(r)
	if session == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	_ = r.ParseMultipartForm(10 << 20) // 10MB max
	guildID := r.FormValue("guild_id")
	if guildID == "" {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	if _, ok := session.AdminGuilds[guildID]; !ok {
		http.Error(w, "You do not have Administrator permissions for this server", http.StatusForbidden)
		return
	}

	corpIDStr := r.FormValue("corp_id")
	corpID, _ := strconv.Atoi(corpIDStr)

	rawText := r.FormValue("wallet_text")

	file, _, err := r.FormFile("wallet_file")
	if err == nil && file != nil {
		defer file.Close()
		content, errRead := io.ReadAll(file)
		if errRead == nil && len(content) > 0 {
			if strings.TrimSpace(rawText) != "" {
				rawText += "\n" + string(content)
			} else {
				rawText = string(content)
			}
		}
	}

	if strings.TrimSpace(rawText) == "" {
		http.Redirect(w, r, "/dashboard/guild?id="+guildID+"&error=Please+paste+wallet+journal+text+or+select+a+file", http.StatusSeeOther)
		return
	}

	imported, dups, corpName, err := donations.ImportWalletJournal(guildID, corpID, rawText)
	if err != nil {
		msg := url.QueryEscape(err.Error())
		http.Redirect(w, r, "/dashboard/guild?id="+guildID+"&error="+msg, http.StatusSeeOther)
		return
	}

	if h.dg != nil {
		db.DiscordLog(h.dg, guildID, fmt.Sprintf("Imported %d donations (%d duplicates skipped) for %s via web dashboard by <@%s>.", imported, dups, corpName, session.UserID))
	}

	redirectURL := fmt.Sprintf("/dashboard/guild?id=%s&success=donations_imported&imported=%d&dups=%d", guildID, imported, dups)
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}
