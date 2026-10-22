package web

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"evemaildiscord/db"

	"github.com/bwmarrin/discordgo"
	_ "github.com/mattn/go-sqlite3"
)

func setupTestDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_web.db")

	database, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}

	_, err = database.Exec(`
		CREATE TABLE IF NOT EXISTS guild_integrations (
			guild_id TEXT PRIMARY KEY,
			eve_character_id INTEGER NOT NULL DEFAULT 0,
			eve_refresh_token TEXT NOT NULL DEFAULT '',
			seat_url TEXT NOT NULL DEFAULT '',
			seat_api_key TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS config (
			guild_id TEXT NOT NULL DEFAULT '',
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			PRIMARY KEY (guild_id, key)
		);
		CREATE TABLE IF NOT EXISTS TrackedCorporations (
			guild_id TEXT NOT NULL,
			role_id TEXT NOT NULL,
			eve_corp_id INTEGER NOT NULL,
			PRIMARY KEY (guild_id, role_id)
		);
		CREATE TABLE IF NOT EXISTS DonationRecord (
			transaction_id INTEGER PRIMARY KEY,
			receiver_corp_id INTEGER NOT NULL,
			donor_id INTEGER NOT NULL,
			donor_type TEXT NOT NULL,
			amount REAL NOT NULL,
			balance REAL NOT NULL DEFAULT 0,
			date DATETIME NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}

	db.DB = database
}

// stubDiscordGuildsAPI overrides discordAPIBase with a test server answering
// GET /users/@me/guilds for bearer token "test_token" (401 for any other token).
func stubDiscordGuildsAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/@me/guilds" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test_token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode([]discordUserGuild{
			{ID: "guild_100", Name: "Alpha Server"},
			{ID: "guild_200", Name: "Beta Server"},
		})
	}))
	t.Cleanup(srv.Close)

	oldBase := discordAPIBase
	discordAPIBase = srv.URL
	t.Cleanup(func() { discordAPIBase = oldBase })
}

// testSessionWithGuilds builds a discordgo session whose state contains the given guild IDs.
func testSessionWithGuilds(guildIDs ...string) *discordgo.Session {
	state := discordgo.NewState()
	for _, id := range guildIDs {
		_ = state.GuildAdd(&discordgo.Guild{ID: id})
	}
	return &discordgo.Session{State: state}
}

func TestUserIsGuildMember(t *testing.T) {
	stubDiscordGuildsAPI(t)

	dg := testSessionWithGuilds("guild_100")

	if ok, err := userIsGuildMember("test_token", "guild_100", dg); err != nil || !ok {
		t.Fatalf("expected member with bot present, got ok=%v err=%v", ok, err)
	}

	if ok, err := userIsGuildMember("test_token", "guild_100", nil); err != nil || ok {
		t.Fatalf("expected bot-not-present with nil dg, got ok=%v err=%v", ok, err)
	}

	if ok, err := userIsGuildMember("test_token", "guild_300", dg); err != nil || ok {
		t.Fatalf("expected non-member false, got ok=%v err=%v", ok, err)
	}

	if ok, err := userIsGuildMember("bad_token", "guild_100", dg); err == nil || ok {
		t.Fatalf("expected API error, got ok=%v err=%v", ok, err)
	}
}

func TestHandleEveLogin_CharacterMode(t *testing.T) {
	os.Setenv("EVE_CLIENT_ID", "test_eve_client_id")
	defer os.Unsetenv("EVE_CLIENT_ID")
	stubDiscordGuildsAPI(t)

	dg := testSessionWithGuilds("guild_100")
	handler := NewWebHandler(dg)

	// Regular member (no admin guilds) can link their own character.
	session, err := Store.CreateSession("user123", "TestUser", "avatar1", "test_token", nil)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	req := httptest.NewRequest("GET", "/auth/eve/login?guild_id=guild_100&mode=character", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
	w := httptest.NewRecorder()

	handler.HandleEveLogin(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect for member character mode, got %d", w.Code)
	}

	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("bad location: %v", err)
	}
	state := u.Query().Get("state")
	data, ok := Store.oauthStates[state]
	if !ok || data.Mode != "character" || data.GuildID != "guild_100" {
		t.Fatalf("expected saved state with mode=character for guild_100, got %+v", data)
	}

	// Non-member is rejected.
	reqForbidden := httptest.NewRequest("GET", "/auth/eve/login?guild_id=guild_300&mode=character", nil)
	reqForbidden.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
	wForbidden := httptest.NewRecorder()

	handler.HandleEveLogin(wForbidden, reqForbidden)

	if wForbidden.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-member, got %d", wForbidden.Code)
	}

	// Admin flow without mode keeps the admin gate and stores no mode.
	adminSession, err := Store.CreateSession("user123", "TestUser", "avatar1", "test_token", map[string]DiscordGuildInfo{
		"guild_100": {ID: "guild_100", Name: "Alpha Server", HasAdmin: true},
	})
	if err != nil {
		t.Fatalf("failed to create admin session: %v", err)
	}

	reqAdmin := httptest.NewRequest("GET", "/auth/eve/login?guild_id=guild_100", nil)
	reqAdmin.AddCookie(&http.Cookie{Name: SessionCookieName, Value: adminSession.ID})
	wAdmin := httptest.NewRecorder()

	handler.HandleEveLogin(wAdmin, reqAdmin)

	if wAdmin.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect for admin flow, got %d", wAdmin.Code)
	}

	uAdmin, err := url.Parse(wAdmin.Header().Get("Location"))
	if err != nil {
		t.Fatalf("bad location: %v", err)
	}
	adminData, ok := Store.oauthStates[uAdmin.Query().Get("state")]
	if !ok || adminData.Mode != "" {
		t.Fatalf("expected admin state without mode, got %+v", adminData)
	}
}

func TestHandleDashboard_MemberGuilds(t *testing.T) {
	setupTestDB(t)
	stubDiscordGuildsAPI(t)

	dg := testSessionWithGuilds("guild_100", "guild_200")
	handler := NewWebHandler(dg)

	adminGuilds := map[string]DiscordGuildInfo{
		"guild_100": {ID: "guild_100", Name: "Alpha Server", HasAdmin: true},
	}

	session, err := Store.CreateSession("user123", "TestUser", "avatar1", "test_token", adminGuilds)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	req := httptest.NewRequest("GET", "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
	w := httptest.NewRecorder()

	handler.HandleDashboard(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Your Servers") || !strings.Contains(body, "Link EVE Character") {
		t.Errorf("expected member guilds section in dashboard body")
	}
	if !strings.Contains(body, "Beta Server") {
		t.Errorf("expected member-only guild in dashboard body")
	}
	// guild_100 is an admin guild, guild_200 is member-only: exactly one character-mode link.
	if n := strings.Count(body, "mode=character"); n != 1 {
		t.Errorf("expected exactly 1 character-mode link, got %d", n)
	}
}

func TestHandleIndex_Unauthenticated(t *testing.T) {
	handler := NewWebHandler(nil)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	handler.HandleIndex(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	body := w.Body.String()
	if !strings.Contains(body, "CNGBot Control Panel") {
		t.Errorf("expected landing page content, got: %s", body)
	}
}

func TestHandleIndex_Authenticated(t *testing.T) {
	session, err := Store.CreateSession("user123", "TestUser", "avatar1", "tok", nil)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	handler := NewWebHandler(nil)
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
	w := httptest.NewRecorder()

	handler.HandleIndex(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect to /dashboard, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/dashboard" {
		t.Fatalf("expected redirect to /dashboard, got %s", loc)
	}
}

func TestHandleDiscordLogin(t *testing.T) {
	os.Setenv("DISCORD_CLIENT_ID", "test_discord_client_id")
	os.Setenv("DISCORD_OAUTH_REDIRECT", "http://localhost:7887/auth/discord/callback")
	defer func() {
		os.Unsetenv("DISCORD_CLIENT_ID")
		os.Unsetenv("DISCORD_OAUTH_REDIRECT")
	}()

	handler := NewWebHandler(nil)
	req := httptest.NewRequest("GET", "/auth/discord/login", nil)
	w := httptest.NewRecorder()

	handler.HandleDiscordLogin(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", resp.StatusCode)
	}

	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "https://discord.com/api/oauth2/authorize") {
		t.Errorf("expected discord authorize url, got: %s", loc)
	}
	if !strings.Contains(loc, "client_id=test_discord_client_id") {
		t.Errorf("expected client_id in location, got: %s", loc)
	}
}

func TestHandleDashboard_And_GuildConfig(t *testing.T) {
	setupTestDB(t)
	stubDiscordGuildsAPI(t)

	adminGuilds := map[string]DiscordGuildInfo{
		"guild_100": {
			ID:          "guild_100",
			Name:        "Alpha Corp Server",
			HasAdmin:    true,
			Permissions: "8",
		},
	}

	session, err := Store.CreateSession("user123", "TestUser", "avatar1", "tok", adminGuilds)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	handler := NewWebHandler(nil)

	// 1. Test Dashboard
	reqDash := httptest.NewRequest("GET", "/dashboard", nil)
	reqDash.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
	wDash := httptest.NewRecorder()

	handler.HandleDashboard(wDash, reqDash)
	if wDash.Code != http.StatusOK {
		t.Fatalf("expected status 200 on dashboard, got %d", wDash.Code)
	}
	if !strings.Contains(wDash.Body.String(), "Alpha Corp Server") {
		t.Errorf("expected guild name in dashboard body")
	}

	// 2. Test Guild Config with access
	reqConfig := httptest.NewRequest("GET", "/dashboard/guild?id=guild_100", nil)
	reqConfig.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
	wConfig := httptest.NewRecorder()

	handler.HandleGuildConfig(wConfig, reqConfig)
	if wConfig.Code != http.StatusOK {
		t.Fatalf("expected status 200 on guild config, got %d", wConfig.Code)
	}
	if !strings.Contains(wConfig.Body.String(), "Alpha Corp Server") {
		t.Errorf("expected guild config for Alpha Corp Server")
	}

	// 3. Test Guild Config without access
	reqForbidden := httptest.NewRequest("GET", "/dashboard/guild?id=guild_999", nil)
	reqForbidden.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
	wForbidden := httptest.NewRecorder()

	handler.HandleGuildConfig(wForbidden, reqForbidden)
	if wForbidden.Code != http.StatusForbidden {
		t.Fatalf("expected status 403 forbidden, got %d", wForbidden.Code)
	}
}

func TestHandleGuildSeatSave_And_EveUnlink(t *testing.T) {
	setupTestDB(t)

	adminGuilds := map[string]DiscordGuildInfo{
		"guild_100": {
			ID:       "guild_100",
			Name:     "Alpha Server",
			HasAdmin: true,
		},
	}

	session, err := Store.CreateSession("user123", "TestUser", "avatar1", "tok", adminGuilds)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	handler := NewWebHandler(nil)

	// 1. Save SeAT configuration
	seatForm := url.Values{
		"guild_id":     {"guild_100"},
		"seat_url":     {"https://seat.testcorp.com"},
		"seat_api_key": {"super_secret_seat_token"},
	}
	reqSeat := httptest.NewRequest("POST", "/dashboard/guild/seat", strings.NewReader(seatForm.Encode()))
	reqSeat.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqSeat.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
	wSeat := httptest.NewRecorder()

	handler.HandleGuildSeatSave(wSeat, reqSeat)
	if wSeat.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect on seat save, got %d", wSeat.Code)
	}

	integ, err := db.GetGuildIntegration("guild_100")
	if err != nil {
		t.Fatalf("failed to get guild integration: %v", err)
	}
	if integ.SeatURL != "https://seat.testcorp.com" || integ.SeatAPIKey != "super_secret_seat_token" {
		t.Fatalf("seat integration mismatch: %+v", integ)
	}

	// 2. Set EVE Auth then test unlink
	err = db.SetGuildEveAuth("guild_100", 991234, "mock_refresh_token")
	if err != nil {
		t.Fatalf("failed to set eve auth: %v", err)
	}

	unlinkForm := url.Values{
		"guild_id": {"guild_100"},
	}
	reqUnlink := httptest.NewRequest("POST", "/dashboard/guild/eve/unlink", strings.NewReader(unlinkForm.Encode()))
	reqUnlink.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqUnlink.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
	wUnlink := httptest.NewRecorder()

	handler.HandleGuildEveUnlink(wUnlink, reqUnlink)
	if wUnlink.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect on unlink, got %d", wUnlink.Code)
	}

	integ, err = db.GetGuildIntegration("guild_100")
	if err != nil || integ.EveCharacterID != 0 || integ.EveRefreshToken != "" {
		t.Fatalf("expected eve integration cleared, got %+v", integ)
	}
}

func TestExtractEveCharacterFromToken_JWT(t *testing.T) {
	// Generate mock JWT with sub="CHARACTER:EVE:12345678" and name="Test Pilot"
	header := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9"
	payload := "eyJzdWIiOiJDSEFSQUNURVI6RVZFOjEyMzQ1Njc4IiwibmFtZSI6IlRlc3QgUGlsb3QifQ" // {"sub":"CHARACTER:EVE:12345678","name":"Test Pilot"}
	sig := "dummy_signature"
	mockJWT := header + "." + payload + "." + sig

	charID, charName := extractEveCharacterFromToken(mockJWT)
	if charID != 12345678 {
		t.Errorf("expected charID 12345678, got %d", charID)
	}
	if charName != "Test Pilot" {
		t.Errorf("expected charName 'Test Pilot', got %s", charName)
	}
}

func TestHandleLogout(t *testing.T) {
	session, err := Store.CreateSession("user123", "TestUser", "avatar1", "tok", nil)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	handler := NewWebHandler(nil)
	req := httptest.NewRequest("GET", "/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
	w := httptest.NewRecorder()

	handler.HandleLogout(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", resp.StatusCode)
	}

	// Session should be deleted
	if Store.GetSession(req) != nil {
		t.Errorf("expected session to be deleted from store")
	}
}

func TestHandleGuildDonationsImport(t *testing.T) {
	setupTestDB(t)

	guildID := "guild_100"
	corpID := 98765432
	_ = db.AddTrackedCorporation(guildID, "role_cult", corpID)

	adminGuilds := map[string]DiscordGuildInfo{
		guildID: {
			ID:       guildID,
			Name:     "Alpha Server",
			HasAdmin: true,
		},
	}

	session, err := Store.CreateSession("user123", "TestUser", "avatar1", "tok", adminGuilds)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	handler := NewWebHandler(nil)

	pastedText := "2026.09.05 20:05\tPlayer Donation\t500.000.000 ISK\t7.782.693.407 ISK\tAmayah Thara deposited cash into Cult of Magik's account"
	form := url.Values{
		"guild_id":    {guildID},
		"corp_id":     {strconv.Itoa(corpID)},
		"wallet_text": {pastedText},
	}

	req := httptest.NewRequest("POST", "/dashboard/guild/donations/import", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
	w := httptest.NewRecorder()

	handler.HandleGuildDonationsImport(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect on donations import, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "success=donations_imported") || !strings.Contains(loc, "imported=1") {
		t.Errorf("unexpected location: %s", loc)
	}
}
