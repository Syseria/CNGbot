package db

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestCreateOrMigrateTables_NewDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer database.Close()

	if err := createOrMigrateTables(database); err != nil {
		t.Fatalf("createOrMigrateTables failed on fresh db: %v", err)
	}

	// Verify guild_id exists on corp_to_role
	_, err = database.Exec("INSERT INTO corp_to_role (guild_id, corp_id, role_id) VALUES (?, ?, ?)", "guild1", 123, "role1")
	if err != nil {
		t.Fatalf("failed to insert into migrated corp_to_role: %v", err)
	}
}

func TestCreateOrMigrateTables_LegacyDB(t *testing.T) {
	t.Setenv("DISCORD_GUILD", "123456789")

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "legacy.db")

	database, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer database.Close()

	// Create legacy schema
	_, err = database.Exec(`
		CREATE TABLE corp_to_role (corp_id INTEGER PRIMARY KEY, role_id TEXT);
		CREATE TABLE config (key TEXT PRIMARY KEY, value TEXT);
		INSERT INTO corp_to_role (corp_id, role_id) VALUES (999, 'role_legacy');
		INSERT INTO config (key, value) VALUES ('log_channel', '112233');
	`)
	if err != nil {
		t.Fatalf("failed to setup legacy schema: %v", err)
	}

	// Run migration
	if err := createOrMigrateTables(database); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// Check migrated data attached to previous DISCORD_GUILD
	var roleID string
	err = database.QueryRow("SELECT role_id FROM corp_to_role WHERE guild_id = '123456789' AND corp_id = 999").Scan(&roleID)
	if err != nil || roleID != "role_legacy" {
		t.Fatalf("expected legacy corp role preserved with guild_id, got %q (err: %v)", roleID, err)
	}

	var logChan string
	err = database.QueryRow("SELECT value FROM config WHERE guild_id = '123456789' AND key = 'log_channel'").Scan(&logChan)
	if err != nil || logChan != "112233" {
		t.Fatalf("expected legacy config preserved with guild_id, got %q (err: %v)", logChan, err)
	}
}

func TestGuildIntegrationsAndMailState(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_integrations.db")

	database, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer database.Close()

	if err := createOrMigrateTables(database); err != nil {
		t.Fatalf("createOrMigrateTables failed: %v", err)
	}

	oldDB := DB
	DB = database
	defer func() { DB = oldDB }()

	// Test GuildIntegration CRUD
	guildID := "guild_123"
	integ, err := GetGuildIntegration(guildID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if integ.EveCharacterID != 0 || integ.EveRefreshToken != "" {
		t.Fatalf("expected empty integration, got %+v", integ)
	}

	err = SetGuildEveAuth(guildID, 98765, "refresh_tok_abc")
	if err != nil {
		t.Fatalf("failed to set eve auth: %v", err)
	}

	err = SetGuildSeatConfig(guildID, "https://seat.example.com", "seat_key_xyz")
	if err != nil {
		t.Fatalf("failed to set seat config: %v", err)
	}

	integ, err = GetGuildIntegration(guildID)
	if err != nil {
		t.Fatalf("failed to get guild integration: %v", err)
	}
	if integ.EveCharacterID != 98765 || integ.EveRefreshToken != "refresh_tok_abc" || integ.SeatURL != "https://seat.example.com" || integ.SeatAPIKey != "seat_key_xyz" {
		t.Fatalf("integration mismatch: %+v", integ)
	}

	all, err := GetAllGuildIntegrations()
	if err != nil || len(all) != 1 {
		t.Fatalf("expected 1 integration, got %d (err: %v)", len(all), err)
	}

	err = DeleteGuildEveAuth(guildID)
	if err != nil {
		t.Fatalf("failed to delete eve auth: %v", err)
	}

	integ, err = GetGuildIntegration(guildID)
	if err != nil || integ.EveCharacterID != 0 || integ.EveRefreshToken != "" {
		t.Fatalf("expected eve auth cleared, got %+v", integ)
	}

	// Test MailState CRUD
	mailState, err := GetMailState(guildID)
	if err != nil || mailState != 0 {
		t.Fatalf("expected initial 0 mail state, got %d (err: %v)", mailState, err)
	}

	err = SetMailState(guildID, 54321)
	if err != nil {
		t.Fatalf("failed to set mail state: %v", err)
	}

	mailState, err = GetMailState(guildID)
	if err != nil || mailState != 54321 {
		t.Fatalf("expected mail state 54321, got %d (err: %v)", mailState, err)
	}
}

func TestFleetInviteState(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_fleet_invite.db")

	database, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer database.Close()

	if err := createOrMigrateTables(database); err != nil {
		t.Fatalf("createOrMigrateTables failed: %v", err)
	}

	oldDB := DB
	DB = database
	defer func() { DB = oldDB }()

	// No row recorded yet
	eveID := 95123
	ts, err := GetFleetLastInvite(eveID)
	if err != nil || ts != 0 {
		t.Fatalf("expected 0 for unknown char, got %d (err: %v)", ts, err)
	}

	if err := SetFleetInvite(eveID, 1700000000); err != nil {
		t.Fatalf("failed to set fleet invite: %v", err)
	}

	ts, err = GetFleetLastInvite(eveID)
	if err != nil || ts != 1700000000 {
		t.Fatalf("expected 1700000000, got %d (err: %v)", ts, err)
	}

	// Second set replaces the value
	if err := SetFleetInvite(eveID, 1700001234); err != nil {
		t.Fatalf("failed to update fleet invite: %v", err)
	}

	ts, err = GetFleetLastInvite(eveID)
	if err != nil || ts != 1700001234 {
		t.Fatalf("expected 1700001234 after replace, got %d (err: %v)", ts, err)
	}
}

func TestCharacterTokens(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_character_tokens.db")

	database, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer database.Close()

	if err := createOrMigrateTables(database); err != nil {
		t.Fatalf("createOrMigrateTables failed: %v", err)
	}

	oldDB := DB
	DB = database
	defer func() { DB = oldDB }()

	// Empty table returns empty map, no error
	tokens, err := GetGuildCharacterTokens("g1")
	if err != nil {
		t.Fatalf("unexpected error on empty table: %v", err)
	}
	if tokens == nil || len(tokens) != 0 {
		t.Fatalf("expected empty map, got %v", tokens)
	}

	// Two characters in g1, one in g2
	if err := SetCharacterToken(1001, "tok_a", "g1", "user_1"); err != nil {
		t.Fatalf("failed to set token for char 1001: %v", err)
	}
	if err := SetCharacterToken(1002, "tok_b", "g1", "user_2"); err != nil {
		t.Fatalf("failed to set token for char 1002: %v", err)
	}
	if err := SetCharacterToken(2001, "tok_c", "g2", "user_3"); err != nil {
		t.Fatalf("failed to set token for char 2001: %v", err)
	}

	tokens, err = GetGuildCharacterTokens("g1")
	if err != nil {
		t.Fatalf("failed to get tokens for g1: %v", err)
	}
	if len(tokens) != 2 || tokens[1001] != "tok_a" || tokens[1002] != "tok_b" {
		t.Fatalf("expected exactly chars 1001/1002 with their tokens, got %v", tokens)
	}

	tokens, err = GetGuildCharacterTokens("g2")
	if err != nil || len(tokens) != 1 || tokens[2001] != "tok_c" {
		t.Fatalf("expected char 2001 with tok_c for g2, got %v (err: %v)", tokens, err)
	}

	// Re-linking replaces the token
	if err := SetCharacterToken(1001, "tok_a_new", "g1", "user_1"); err != nil {
		t.Fatalf("failed to relink char 1001: %v", err)
	}

	tokens, err = GetGuildCharacterTokens("g1")
	if err != nil || len(tokens) != 2 || tokens[1001] != "tok_a_new" {
		t.Fatalf("expected replaced token tok_a_new, got %v (err: %v)", tokens, err)
	}
}

func TestDonationsDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_donations.db")

	database, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer database.Close()

	if err := createOrMigrateTables(database); err != nil {
		t.Fatalf("createOrMigrateTables failed: %v", err)
	}

	oldDB := DB
	DB = database
	defer func() { DB = oldDB }()

	guildID := "guild_donations_test"

	// 1. Test GuildDonationSettings
	settings, err := GetGuildDonationSettings(guildID)
	if err != nil {
		t.Fatalf("unexpected error getting settings: %v", err)
	}
	if settings != nil {
		t.Fatalf("expected nil settings for unset guild, got %+v", settings)
	}

	chanID := "chan_123"
	if err := SetGuildDonationChannel(guildID, &chanID); err != nil {
		t.Fatalf("failed to set donation channel: %v", err)
	}

	settings, err = GetGuildDonationSettings(guildID)
	if err != nil || settings == nil || settings.ChannelID == nil || *settings.ChannelID != "chan_123" {
		t.Fatalf("expected chan_123, got %+v", settings)
	}

	if err := SetGuildDonationChannel(guildID, nil); err != nil {
		t.Fatalf("failed to disable donation channel: %v", err)
	}

	settings, err = GetGuildDonationSettings(guildID)
	if err != nil || settings != nil {
		t.Fatalf("expected nil settings after disable, got %+v", settings)
	}

	// 2. Test TrackedCorporations
	roleID := "role_corp_1"
	eveCorpID := 98000001
	if err := AddTrackedCorporation(guildID, roleID, eveCorpID); err != nil {
		t.Fatalf("failed to add tracked corp: %v", err)
	}

	tc, err := GetTrackedCorporation(guildID, roleID)
	if err != nil || tc == nil || tc.EveCorpID != eveCorpID {
		t.Fatalf("expected tracked corp with id %d, got %+v", eveCorpID, tc)
	}

	corps, err := GetTrackedCorporationsForGuild(guildID)
	if err != nil || len(corps) != 1 || corps[0].EveCorpID != eveCorpID {
		t.Fatalf("expected 1 tracked corp, got %+v", corps)
	}

	uniqueCorps, err := GetAllUniqueTrackedCorpIDs()
	if err != nil || len(uniqueCorps) != 1 || uniqueCorps[0] != eveCorpID {
		t.Fatalf("expected 1 unique corp id, got %+v", uniqueCorps)
	}

	guilds, err := GetGuildsTrackingCorp(eveCorpID)
	if err != nil || len(guilds) != 1 || guilds[0] != guildID {
		t.Fatalf("expected guildID, got %+v", guilds)
	}

	// 3. Test DonationRecord & Leaderboard
	exists, err := DonationRecordExists(111)
	if err != nil || exists {
		t.Fatalf("expected false, got %v (err: %v)", exists, err)
	}

	rec1 := DonationRecord{
		TransactionID:  111,
		ReceiverCorpID: eveCorpID,
		DonorID:        1001,
		DonorType:      "character",
		Amount:         5000000,
	}
	if err := InsertDonationRecord(rec1); err != nil {
		t.Fatalf("failed to insert donation record 1: %v", err)
	}

	exists, err = DonationRecordExists(111)
	if err != nil || !exists {
		t.Fatalf("expected true, got %v", exists)
	}

	rec2 := DonationRecord{
		TransactionID:  112,
		ReceiverCorpID: eveCorpID,
		DonorID:        1001,
		DonorType:      "character",
		Amount:         10000000,
	}
	if err := InsertDonationRecord(rec2); err != nil {
		t.Fatalf("failed to insert donation record 2: %v", err)
	}

	rec3 := DonationRecord{
		TransactionID:  113,
		ReceiverCorpID: eveCorpID,
		DonorID:        2001,
		DonorType:      "corporation",
		Amount:         20000000,
	}
	if err := InsertDonationRecord(rec3); err != nil {
		t.Fatalf("failed to insert donation record 3: %v", err)
	}

	leaderboard, err := GetDonationsLeaderboard(guildID, 50)
	if err != nil {
		t.Fatalf("failed to get leaderboard: %v", err)
	}
	if len(leaderboard) != 2 {
		t.Fatalf("expected 2 leaderboard entries, got %d", len(leaderboard))
	}
	if leaderboard[0].DonorID != 2001 || leaderboard[0].TotalAmount != 20000000 || leaderboard[0].DonationCount != 1 {
		t.Fatalf("unexpected rank 1: %+v", leaderboard[0])
	}
	if leaderboard[1].DonorID != 1001 || leaderboard[1].TotalAmount != 15000000 || leaderboard[1].DonationCount != 2 {
		t.Fatalf("unexpected rank 2: %+v", leaderboard[1])
	}

	// 4. Test RemoveTrackedCorporation
	removed, err := RemoveTrackedCorporation(guildID, roleID)
	if err != nil || !removed {
		t.Fatalf("expected removed=true, got %v", removed)
	}

	tc, err = GetTrackedCorporation(guildID, roleID)
	if err != nil || tc != nil {
		t.Fatalf("expected nil after removal, got %+v", tc)
	}
}

func TestWandererTrackedAndState(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := sql.Open("sqlite3", filepath.Join(tmpDir, "test_wanderer.db"))
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer database.Close()

	if err := createOrMigrateTables(database); err != nil {
		t.Fatalf("createOrMigrateTables failed: %v", err)
	}

	oldDB := DB
	DB = database
	defer func() { DB = oldDB }()

	// Tracked systems CRUD
	found, err := GetWandererTrackedByName("g1", "J111011")
	if err != nil || found != nil {
		t.Fatalf("expected no tracked system, got %+v (err: %v)", found, err)
	}

	if err := AddWandererTrackedSystem("g1", 31001234, "J111011"); err != nil {
		t.Fatalf("failed to add tracked system: %v", err)
	}
	// duplicate insert must be a no-op (PK guild_id+solar_system_id)
	if err := AddWandererTrackedSystem("g1", 31001234, "J111011"); err != nil {
		t.Fatalf("duplicate insert errored: %v", err)
	}
	if err := AddWandererTrackedSystem("g2", 31009999, "J100809"); err != nil {
		t.Fatalf("failed to add tracked system: %v", err)
	}

	all, err := ListWandererTrackedSystems()
	if err != nil || len(all) != 2 {
		t.Fatalf("expected 2 tracked systems, got %d (err: %v)", len(all), err)
	}

	// case-insensitive lookup by name
	found, err = GetWandererTrackedByName("g1", "j111011")
	if err != nil || found == nil || found.SolarSystemID != 31001234 {
		t.Fatalf("expected case-insensitive lookup hit, got %+v (err: %v)", found, err)
	}

	if err := RemoveWandererTrackedSystem("g1", 31001234); err != nil {
		t.Fatalf("failed to remove tracked system: %v", err)
	}
	all, err = ListWandererTrackedSystems()
	if err != nil || len(all) != 1 {
		t.Fatalf("expected 1 tracked system after remove, got %d (err: %v)", len(all), err)
	}

	// Poll state
	if _, ok, err := GetWandererState("g1"); ok || err != nil {
		t.Fatalf("expected no state, got ok=%v (err: %v)", ok, err)
	}
	now := time.Now().UTC()
	if err := SetWandererState("g1", now); err != nil {
		t.Fatalf("failed to set state: %v", err)
	}
	got, ok, err := GetWandererState("g1")
	if err != nil || !ok || got.Sub(now) > time.Minute || now.Sub(got) > time.Minute {
		t.Fatalf("state mismatch: ok=%v got=%v want~%v (err: %v)", ok, got, now, err)
	}

	// DSN config
	if err := SetGuildWandererDSN("g1", "postgres://user:pass@host:5432/wanderer"); err != nil {
		t.Fatalf("failed to set dsn: %v", err)
	}
	dsn, err := GetGuildWandererDSN("g1")
	if err != nil || dsn != "postgres://user:pass@host:5432/wanderer" {
		t.Fatalf("dsn mismatch: %q (err: %v)", dsn, err)
	}
	if err := SetGuildWandererDSN("g1", ""); err != nil {
		t.Fatalf("failed to clear dsn: %v", err)
	}
	if dsn, err := GetGuildWandererDSN("g1"); err != nil || dsn != "" {
		t.Fatalf("expected empty dsn after clear, got %q (err: %v)", dsn, err)
	}
}

func TestWandererSeenPairs(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := sql.Open("sqlite3", filepath.Join(tmpDir, "test_seen_pairs.db"))
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer database.Close()

	if err := createOrMigrateTables(database); err != nil {
		t.Fatalf("createOrMigrateTables failed: %v", err)
	}

	oldDB := DB
	DB = database
	defer func() { DB = oldDB }()

	seen, err := IsWandererPairSeen("g1", 100, 200)
	if err != nil || seen {
		t.Fatalf("expected unseen pair, got %v (err: %v)", seen, err)
	}

	if err := MarkWandererPairSeen("g1", 100, 200); err != nil {
		t.Fatalf("failed to mark pair: %v", err)
	}
	if err := MarkWandererPairSeen("g1", 100, 200); err != nil {
		t.Fatalf("duplicate mark errored: %v", err)
	}

	// reversed order must hit the same normalized pair
	seen, err = IsWandererPairSeen("g1", 200, 100)
	if err != nil || !seen {
		t.Fatalf("expected normalized pair hit, got %v (err: %v)", seen, err)
	}

	// other pairs and guilds are unaffected
	if seen, err := IsWandererPairSeen("g1", 300, 400); err != nil || seen {
		t.Fatalf("expected different pair unseen, got %v (err: %v)", seen, err)
	}
	if err := MarkWandererPairSeen("g2", 100, 200); err != nil {
		t.Fatalf("failed to mark pair for g2: %v", err)
	}
	if seen, err := IsWandererPairSeen("g1", 100, 200); err != nil || !seen {
		t.Fatalf("expected g1 pair still seen, got %v (err: %v)", seen, err)
	}
}

func TestProjectsPostedIDs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_posted_ids.db")

	database, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer database.Close()

	// Pre-create projects_state with the legacy schema (no posted-ID columns)
	// to verify the migration adds them.
	if _, err := database.Exec(`
		CREATE TABLE projects_state (
			guild_id TEXT PRIMARY KEY,
			projects_watermark TEXT NOT NULL DEFAULT '',
			jobs_watermark TEXT NOT NULL DEFAULT ''
		);
	`); err != nil {
		t.Fatalf("failed to create legacy projects_state: %v", err)
	}

	if err := createOrMigrateTables(database); err != nil {
		t.Fatalf("createOrMigrateTables failed: %v", err)
	}

	oldDB := DB
	DB = database
	defer func() { DB = oldDB }()

	// No row yet: empty set, no error.
	ids, err := GetProjectsPostedIDs("g1", "project")
	if err != nil || len(ids) != 0 {
		t.Fatalf("expected empty posted set, got %v (err: %v)", ids, err)
	}

	// Roundtrip for one kind must not leak into the other.
	if err := SetProjectsPostedIDs("g1", "project", map[string]bool{"a": true, "b": true}); err != nil {
		t.Fatalf("failed to set posted ids: %v", err)
	}
	got, err := GetProjectsPostedIDs("g1", "project")
	if err != nil {
		t.Fatalf("failed to get posted ids: %v", err)
	}
	if !got["a"] || !got["b"] || len(got) != 2 {
		t.Fatalf("posted ids mismatch: %v", got)
	}
	jobs, err := GetProjectsPostedIDs("g1", "freelance job")
	if err != nil || len(jobs) != 0 {
		t.Fatalf("expected empty jobs posted set, got %v (err: %v)", jobs, err)
	}

	// Empty map clears the stored set.
	if err := SetProjectsPostedIDs("g1", "project", map[string]bool{}); err != nil {
		t.Fatalf("failed to clear posted ids: %v", err)
	}
	got, err = GetProjectsPostedIDs("g1", "project")
	if err != nil || len(got) != 0 {
		t.Fatalf("expected cleared posted set, got %v (err: %v)", got, err)
	}
}
