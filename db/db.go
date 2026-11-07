package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	_ "github.com/mattn/go-sqlite3"
)

var (
	DB         *sql.DB
	queueMutex sync.Mutex
	cmdQueue   []QueuedCommand
)

type QueuedCommand struct {
	Session   *discordgo.Session
	GuildID   string
	ChannelID string
	Execute   func() error
}

func InitDB() error {
	var err error
	DB, err = sql.Open("sqlite3", "./data.db?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("error opening database: %w", err)
	}

	DB.SetMaxOpenConns(1)

	if err := createOrMigrateTables(DB); err != nil {
		return fmt.Errorf("error migrating tables: %w", err)
	}

	return nil
}

func createOrMigrateTables(database *sql.DB) error {
	// 1. Ensure global tables exist
	_, err := database.Exec(`
		CREATE TABLE IF NOT EXISTS character_to_discord (
			eve_id INTEGER PRIMARY KEY,
			discord_id TEXT
		);
		CREATE TABLE IF NOT EXISTS guild_integrations (
			guild_id TEXT PRIMARY KEY,
			eve_character_id INTEGER NOT NULL DEFAULT 0,
			eve_refresh_token TEXT NOT NULL DEFAULT '',
			seat_url TEXT NOT NULL DEFAULT '',
			seat_api_key TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS mail_state (
			guild_id TEXT PRIMARY KEY,
			last_mail_id INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS GuildDonationSettings (
			guild_id TEXT PRIMARY KEY,
			channel_id TEXT
		);
		CREATE TABLE IF NOT EXISTS TrackedCorporations (
			guild_id TEXT NOT NULL,
			role_id TEXT NOT NULL,
			eve_corp_id INTEGER NOT NULL,
			PRIMARY KEY (guild_id, role_id)
		);
		CREATE INDEX IF NOT EXISTS idx_tracked_corps_guild ON TrackedCorporations(guild_id);
		CREATE INDEX IF NOT EXISTS idx_tracked_corps_corp ON TrackedCorporations(eve_corp_id);
		CREATE TABLE IF NOT EXISTS DonationRecord (
			transaction_id INTEGER PRIMARY KEY,
			receiver_corp_id INTEGER NOT NULL,
			donor_id INTEGER NOT NULL,
			donor_type TEXT NOT NULL,
			amount REAL NOT NULL,
			balance REAL NOT NULL DEFAULT 0,
			date DATETIME NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_donation_record_receiver ON DonationRecord(receiver_corp_id);
		CREATE INDEX IF NOT EXISTS idx_donation_record_donor ON DonationRecord(donor_id);
		CREATE TABLE IF NOT EXISTS fleet_invite_state (
			eve_id INTEGER PRIMARY KEY,
			last_invite_unix INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS character_tokens (
			eve_character_id INTEGER PRIMARY KEY,
			refresh_token TEXT NOT NULL DEFAULT '',
			guild_id TEXT NOT NULL DEFAULT '',
			discord_user_id TEXT NOT NULL DEFAULT '',
			linked_at DATETIME
		);
		CREATE TABLE IF NOT EXISTS projects_state (
			guild_id TEXT PRIMARY KEY,
			projects_watermark TEXT NOT NULL DEFAULT '',
			jobs_watermark TEXT NOT NULL DEFAULT '',
			projects_posted TEXT NOT NULL DEFAULT '',
			jobs_posted TEXT NOT NULL DEFAULT ''
		);
	`)
	if err != nil {
		return fmt.Errorf("error creating base tables: %w", err)
	}

	// Ensure balance column exists on DonationRecord
	var hasBalance bool
	colRows, err := database.Query("PRAGMA table_info(DonationRecord)")
	if err == nil {
		for colRows.Next() {
			var cid int
			var name, colType string
			var notNull, pk int
			var dfltValue *string
			if colRows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk) == nil {
				if strings.EqualFold(name, "balance") {
					hasBalance = true
				}
			}
		}
		_ = colRows.Close()
		if !hasBalance {
			_, _ = database.Exec("ALTER TABLE DonationRecord ADD COLUMN balance REAL NOT NULL DEFAULT 0;")
		}
	}

	// Ensure posted-ID columns exist on projects_state
	colRows, err = database.Query("PRAGMA table_info(projects_state)")
	if err == nil {
		var cols []string
		for colRows.Next() {
			var cid int
			var name, colType string
			var notNull, pk int
			var dfltValue *string
			if colRows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk) == nil {
				cols = append(cols, strings.ToLower(name))
			}
		}
		_ = colRows.Close()
		for _, col := range []string{"projects_posted", "jobs_posted"} {
			if !slices.Contains(cols, col) {
				_, _ = database.Exec(fmt.Sprintf("ALTER TABLE projects_state ADD COLUMN %s TEXT NOT NULL DEFAULT '';", col))
			}
		}
	}

	// 2. Define all guild-scoped tables
	type tableDef struct {
		name      string
		schema    string
		colSelect string
	}

	guildTables := []tableDef{
		{
			name:      "corp_to_role",
			schema:    "CREATE TABLE corp_to_role (guild_id TEXT NOT NULL DEFAULT '', corp_id INTEGER NOT NULL, role_id TEXT NOT NULL, PRIMARY KEY (guild_id, corp_id));",
			colSelect: "corp_id, role_id",
		},
		{
			name:      "alliance_to_role",
			schema:    "CREATE TABLE alliance_to_role (guild_id TEXT NOT NULL DEFAULT '', alliance_id INTEGER NOT NULL, role_id TEXT NOT NULL, PRIMARY KEY (guild_id, alliance_id));",
			colSelect: "alliance_id, role_id",
		},
		{
			name:      "config",
			schema:    "CREATE TABLE config (guild_id TEXT NOT NULL DEFAULT '', key TEXT NOT NULL, value TEXT NOT NULL, PRIMARY KEY (guild_id, key));",
			colSelect: "key, value",
		},
		{
			name:      "excluded_users",
			schema:    "CREATE TABLE excluded_users (guild_id TEXT NOT NULL DEFAULT '', discord_id TEXT NOT NULL, PRIMARY KEY (guild_id, discord_id));",
			colSelect: "discord_id",
		},
		{
			name:      "admin_roles",
			schema:    "CREATE TABLE admin_roles (guild_id TEXT NOT NULL DEFAULT '', role_id TEXT NOT NULL, PRIMARY KEY (guild_id, role_id));",
			colSelect: "role_id",
		},
		{
			name:      "auto_map_attempts",
			schema:    "CREATE TABLE auto_map_attempts (guild_id TEXT NOT NULL DEFAULT '', discord_id TEXT NOT NULL, search_name TEXT, PRIMARY KEY (guild_id, discord_id));",
			colSelect: "discord_id, search_name",
		},
		{
			name:      "excluded_mappings",
			schema:    "CREATE TABLE excluded_mappings (guild_id TEXT NOT NULL DEFAULT '', discord_id TEXT NOT NULL, PRIMARY KEY (guild_id, discord_id));",
			colSelect: "discord_id",
		},
		{
			name:      "greet_on_role",
			schema:    "CREATE TABLE greet_on_role (guild_id TEXT NOT NULL DEFAULT '', role_id TEXT NOT NULL, PRIMARY KEY (guild_id, role_id));",
			colSelect: "role_id",
		},
		{
			name:      "standing_to_role",
			schema:    "CREATE TABLE standing_to_role (guild_id TEXT NOT NULL DEFAULT '', standing_type TEXT NOT NULL, role_id TEXT NOT NULL, PRIMARY KEY (guild_id, standing_type));",
			colSelect: "standing_type, role_id",
		},
		{
			name:      "excluded_corp_standings",
			schema:    "CREATE TABLE excluded_corp_standings (guild_id TEXT NOT NULL DEFAULT '', corp_id INTEGER NOT NULL, PRIMARY KEY (guild_id, corp_id));",
			colSelect: "corp_id",
		},
		{
			name:      "pending_2fa",
			schema:    "CREATE TABLE pending_2fa (guild_id TEXT NOT NULL DEFAULT '', discord_id TEXT NOT NULL, eve_id INTEGER, char_name TEXT, code TEXT, expires_at DATETIME, PRIMARY KEY (guild_id, discord_id));",
			colSelect: "discord_id, eve_id, char_name, code, expires_at",
		},
		{
			name:      "excluded_2fa_roles",
			schema:    "CREATE TABLE excluded_2fa_roles (guild_id TEXT NOT NULL DEFAULT '', role_id TEXT NOT NULL, PRIMARY KEY (guild_id, role_id));",
			colSelect: "role_id",
		},
		{
			name:      "verified_2fa_users",
			schema:    "CREATE TABLE verified_2fa_users (guild_id TEXT NOT NULL DEFAULT '', discord_id TEXT NOT NULL, verified_at DATETIME, PRIMARY KEY (guild_id, discord_id));",
			colSelect: "discord_id, verified_at",
		},
		{
			name:      "seat_needed_roles",
			schema:    "CREATE TABLE seat_needed_roles (guild_id TEXT NOT NULL DEFAULT '', role_id TEXT NOT NULL, PRIMARY KEY (guild_id, role_id));",
			colSelect: "role_id",
		},
		{
			name:      "alliance_required_corp_roles",
			schema:    "CREATE TABLE alliance_required_corp_roles (guild_id TEXT NOT NULL DEFAULT '', role_id TEXT NOT NULL, PRIMARY KEY (guild_id, role_id));",
			colSelect: "role_id",
		},
		{
			name:      "wanderer_tracked",
			schema:    "CREATE TABLE wanderer_tracked (guild_id TEXT NOT NULL DEFAULT '', solar_system_id TEXT NOT NULL, system_name TEXT NOT NULL, PRIMARY KEY (guild_id, solar_system_id));",
			colSelect: "solar_system_id, system_name",
		},
		{
			name:      "wanderer_state",
			schema:    "CREATE TABLE wanderer_state (guild_id TEXT NOT NULL DEFAULT '', last_seen TEXT NOT NULL, PRIMARY KEY (guild_id));",
			colSelect: "last_seen",
		},
		{
			name:      "wanderer_seen_pairs",
			schema:    "CREATE TABLE wanderer_seen_pairs (guild_id TEXT NOT NULL DEFAULT '', src_id TEXT NOT NULL, dst_id TEXT NOT NULL, PRIMARY KEY (guild_id, src_id, dst_id));",
			colSelect: "src_id, dst_id",
		},
	}

	for _, t := range guildTables {
		if err := ensureGuildTable(database, t.name, t.schema, t.colSelect); err != nil {
			return fmt.Errorf("error migrating table %s: %w", t.name, err)
		}
	}

	return nil
}

func ensureGuildTable(database *sql.DB, tableName, schema, cols string) error {
	// Check if table exists
	var count int
	err := database.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tableName).Scan(&count)
	if err != nil {
		return err
	}

	if count == 0 {
		_, err := database.Exec(schema)
		return err
	}

	// Check if guild_id column exists
	hasGuildID := false
	rows, err := database.Query(fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dfltValue *string
		if rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk) == nil {
			if strings.EqualFold(name, "guild_id") {
				hasGuildID = true
			}
		}
	}
	_ = rows.Close()

	if hasGuildID {
		return nil
	}

	// Migrate old table to new schema with guild_id
	log.Printf("Migrating table %s to include guild_id...", tableName)
	tempName := "old_" + tableName
	_, err = database.Exec(fmt.Sprintf("ALTER TABLE %s RENAME TO %s;", tableName, tempName))
	if err != nil {
		return fmt.Errorf("failed to rename %s: %w", tableName, err)
	}

	_, err = database.Exec(schema)
	if err != nil {
		return fmt.Errorf("failed to create new %s: %w", tableName, err)
	}

	// If DISCORD_GUILD env var was set previously, preserve it as default guild for migrated data
	fallbackGuildID := strings.TrimSpace(os.Getenv("DISCORD_GUILD"))
	copySQL := fmt.Sprintf("INSERT INTO %s (guild_id, %s) SELECT ?, %s FROM %s;", tableName, cols, cols, tempName)
	_, err = database.Exec(copySQL, fallbackGuildID)
	if err != nil {
		return fmt.Errorf("failed to copy data for %s: %w", tableName, err)
	}

	_, err = database.Exec(fmt.Sprintf("DROP TABLE %s;", tempName))
	if err != nil {
		return fmt.Errorf("failed to drop %s: %w", tempName, err)
	}

	return nil
}

func ExecuteOrQueue(s *discordgo.Session, channelID string, f func() error) {
	err := f()
	if err != nil && IsLockedError(err) {
		if channelID != "" {
			_, _ = s.ChannelMessageSend(channelID, "Currently busy with something else. Your command has been added to the queue")
		}
		queueMutex.Lock()
		cmdQueue = append(cmdQueue, QueuedCommand{
			Session:   s,
			ChannelID: channelID,
			Execute:   f,
		})
		queueMutex.Unlock()
		return
	}
	if err != nil {
		if channelID != "" {
			_, _ = s.ChannelMessageSend(channelID, fmt.Sprintf("Error executing command: %v", err))
		}
	}
}

func IsLockedError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "database is locked") || strings.Contains(errStr, "busy")
}

func ProcessQueue() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lockStartTime time.Time

	for range ticker.C {
		queueMutex.Lock()
		if len(cmdQueue) == 0 {
			lockStartTime = time.Time{}
			queueMutex.Unlock()
			continue
		}

		task := cmdQueue[0]
		queueMutex.Unlock()

		err := task.Execute()
		if err != nil {
			if IsLockedError(err) {
				if lockStartTime.IsZero() {
					lockStartTime = time.Now()
				} else if time.Since(lockStartTime) >= 5*time.Minute {
					msg := "Database has been locked for 5+ minutes. Forcibly breaking the database lock..."
					DiscordLog(task.Session, task.GuildID, msg)

					_ = DB.Close()

					var errOpen error
					DB, errOpen = sql.Open("sqlite3", "./data.db")
					if errOpen != nil {
						log.Printf("Fatal: failed to reopen DB after breaking lock: %v", errOpen)
					}

					lockStartTime = time.Time{}
				}
				log.Println("Database is still locked, keeping command in queue...")
				continue
			}
			if task.ChannelID != "" {
				_, _ = task.Session.ChannelMessageSend(task.ChannelID, fmt.Sprintf("Error executing queued command: %v", err))
			}
		}

		queueMutex.Lock()
		cmdQueue = cmdQueue[1:]
		queueMutex.Unlock()
		lockStartTime = time.Time{}
	}
}

func DiscordLog(dg *discordgo.Session, guildID string, msg string) {
	log.Println(msg)
	if dg == nil {
		return
	}

	if guildID != "" {
		var val string
		err := DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'post_logs'", guildID).Scan(&val)
		if err == sql.ErrNoRows || val == "1" {
			var logChannel string
			errChannel := DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'log_channel'", guildID).Scan(&logChannel)
			if errChannel == nil && logChannel != "" {
				_, _ = dg.ChannelMessageSend(logChannel, msg)
			}
		}
		return
	}

	// If guildID is empty, broadcast to all configured log channels
	rows, err := DB.Query("SELECT guild_id, value FROM config WHERE key = 'log_channel'")
	if err != nil {
		return
	}
	defer func() { _ = rows.Close() }()

	type guildLog struct {
		guildID string
		channel string
	}
	var targets []guildLog
	for rows.Next() {
		var gid, ch string
		if rows.Scan(&gid, &ch) == nil && ch != "" {
			targets = append(targets, guildLog{guildID: gid, channel: ch})
		}
	}

	for _, t := range targets {
		var val string
		err := DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'post_logs'", t.guildID).Scan(&val)
		if err == sql.ErrNoRows || val == "1" {
			_, _ = dg.ChannelMessageSend(t.channel, msg)
		}
	}
}

type GuildIntegration struct {
	GuildID         string `json:"guild_id"`
	EveCharacterID  int    `json:"eve_character_id"`
	EveRefreshToken string `json:"eve_refresh_token"`
	SeatURL         string `json:"seat_url"`
	SeatAPIKey      string `json:"seat_api_key"`
}

func GetGuildIntegration(guildID string) (GuildIntegration, error) {
	var g GuildIntegration
	g.GuildID = guildID
	if DB == nil {
		return g, fmt.Errorf("database not initialized")
	}
	err := DB.QueryRow("SELECT eve_character_id, eve_refresh_token, seat_url, seat_api_key FROM guild_integrations WHERE guild_id = ?", guildID).
		Scan(&g.EveCharacterID, &g.EveRefreshToken, &g.SeatURL, &g.SeatAPIKey)
	if err != nil {
		if err == sql.ErrNoRows {
			return g, nil
		}
		return g, err
	}
	return g, nil
}

func GetAllGuildIntegrations() ([]GuildIntegration, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	rows, err := DB.Query("SELECT guild_id, eve_character_id, eve_refresh_token, seat_url, seat_api_key FROM guild_integrations")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []GuildIntegration
	for rows.Next() {
		var g GuildIntegration
		if err := rows.Scan(&g.GuildID, &g.EveCharacterID, &g.EveRefreshToken, &g.SeatURL, &g.SeatAPIKey); err == nil {
			results = append(results, g)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func SetGuildIntegration(g GuildIntegration) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := DB.Exec(`
		INSERT INTO guild_integrations (guild_id, eve_character_id, eve_refresh_token, seat_url, seat_api_key)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(guild_id) DO UPDATE SET
			eve_character_id = excluded.eve_character_id,
			eve_refresh_token = excluded.eve_refresh_token,
			seat_url = excluded.seat_url,
			seat_api_key = excluded.seat_api_key
	`, g.GuildID, g.EveCharacterID, g.EveRefreshToken, g.SeatURL, g.SeatAPIKey)
	return err
}

func SetGuildEveAuth(guildID string, characterID int, refreshToken string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := DB.Exec(`
		INSERT INTO guild_integrations (guild_id, eve_character_id, eve_refresh_token)
		VALUES (?, ?, ?)
		ON CONFLICT(guild_id) DO UPDATE SET
			eve_character_id = excluded.eve_character_id,
			eve_refresh_token = excluded.eve_refresh_token
	`, guildID, characterID, refreshToken)
	return err
}

func DeleteGuildEveAuth(guildID string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := DB.Exec(`
		UPDATE guild_integrations SET eve_character_id = 0, eve_refresh_token = '' WHERE guild_id = ?
	`, guildID)
	return err
}

func SetGuildSeatConfig(guildID string, seatURL, seatAPIKey string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := DB.Exec(`
		INSERT INTO guild_integrations (guild_id, seat_url, seat_api_key)
		VALUES (?, ?, ?)
		ON CONFLICT(guild_id) DO UPDATE SET
			seat_url = excluded.seat_url,
			seat_api_key = excluded.seat_api_key
	`, guildID, seatURL, seatAPIKey)
	return err
}

func GetMailState(guildID string) (int, error) {
	if DB == nil {
		return 0, fmt.Errorf("database not initialized")
	}
	var lastID int
	err := DB.QueryRow("SELECT last_mail_id FROM mail_state WHERE guild_id = ?", guildID).Scan(&lastID)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	return lastID, nil
}

func SetMailState(guildID string, lastMailID int) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := DB.Exec(`
		INSERT INTO mail_state (guild_id, last_mail_id)
		VALUES (?, ?)
		ON CONFLICT(guild_id) DO UPDATE SET
			last_mail_id = excluded.last_mail_id
	`, guildID, lastMailID)
	return err
}

// Wanderer tracking

type WandererTrackedSystem struct {
	GuildID       string
	SolarSystemID int64
	SystemName    string
}

func AddWandererTrackedSystem(guildID string, solarSystemID int64, systemName string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := DB.Exec("INSERT INTO wanderer_tracked (guild_id, solar_system_id, system_name) VALUES (?, ?, ?) ON CONFLICT(guild_id, solar_system_id) DO NOTHING", guildID, solarSystemID, systemName)
	return err
}

func GetWandererTrackedByName(guildID, systemName string) (*WandererTrackedSystem, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	var ts WandererTrackedSystem
	err := DB.QueryRow("SELECT guild_id, solar_system_id, system_name FROM wanderer_tracked WHERE guild_id = ? AND lower(system_name) = lower(?)", guildID, systemName).Scan(&ts.GuildID, &ts.SolarSystemID, &ts.SystemName)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ts, nil
}

func RemoveWandererTrackedSystem(guildID string, solarSystemID int64) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := DB.Exec("DELETE FROM wanderer_tracked WHERE guild_id = ? AND solar_system_id = ?", guildID, solarSystemID)
	return err
}

func ListWandererTrackedSystems() ([]WandererTrackedSystem, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	rows, err := DB.Query("SELECT guild_id, solar_system_id, system_name FROM wanderer_tracked")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WandererTrackedSystem
	for rows.Next() {
		var ts WandererTrackedSystem
		if err := rows.Scan(&ts.GuildID, &ts.SolarSystemID, &ts.SystemName); err != nil {
			return nil, err
		}
		out = append(out, ts)
	}
	return out, rows.Err()
}

func GetWandererState(guildID string) (time.Time, bool, error) {
	if DB == nil {
		return time.Time{}, false, fmt.Errorf("database not initialized")
	}
	var raw string
	err := DB.QueryRow("SELECT last_seen FROM wanderer_state WHERE guild_id = ?", guildID).Scan(&raw)
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false, err
	}
	return t, true, nil
}

func SetWandererState(guildID string, t time.Time) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := DB.Exec("INSERT INTO wanderer_state (guild_id, last_seen) VALUES (?, ?) ON CONFLICT(guild_id) DO UPDATE SET last_seen = excluded.last_seen", guildID, t.UTC().Format(time.RFC3339Nano))
	return err
}

func SetGuildWandererDSN(guildID, dsn string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	if dsn == "" {
		_, err := DB.Exec("DELETE FROM config WHERE guild_id = ? AND key = 'wanderer_dsn'", guildID)
		return err
	}
	_, err := DB.Exec("INSERT OR REPLACE INTO config (guild_id, key, value) VALUES (?, 'wanderer_dsn', ?)", guildID, dsn)
	return err
}

func GetGuildWandererDSN(guildID string) (string, error) {
	if DB == nil {
		return "", fmt.Errorf("database not initialized")
	}
	var dsn string
	err := DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'wanderer_dsn'", guildID).Scan(&dsn)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return dsn, err
}

// IsWandererPairSeen reports whether a connection between two systems was
// already announced for a guild. Endpoints are normalized so (A,B) == (B,A).
func IsWandererPairSeen(guildID string, srcID, dstID int64) (bool, error) {
	if DB == nil {
		return false, fmt.Errorf("database not initialized")
	}
	if srcID > dstID {
		srcID, dstID = dstID, srcID
	}
	var one int
	err := DB.QueryRow("SELECT 1 FROM wanderer_seen_pairs WHERE guild_id = ? AND src_id = ? AND dst_id = ?", guildID, srcID, dstID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// MarkWandererPairSeen records a connection pair as announced for a guild.
func MarkWandererPairSeen(guildID string, srcID, dstID int64) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	if srcID > dstID {
		srcID, dstID = dstID, srcID
	}
	_, err := DB.Exec("INSERT OR IGNORE INTO wanderer_seen_pairs (guild_id, src_id, dst_id) VALUES (?, ?, ?)", guildID, srcID, dstID)
	return err
}

// GetFleetLastInvite returns the unix timestamp of the last auto fleet invite
// attempt for the given EVE character, or 0 if no attempt was recorded.
func GetFleetLastInvite(eveID int) (int64, error) {
	var ts int64
	err := DB.QueryRow("SELECT last_invite_unix FROM fleet_invite_state WHERE eve_id = ?", eveID).Scan(&ts)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return ts, err
}

// SetFleetInvite records the unix timestamp of the latest auto fleet invite attempt for the character.
func SetFleetInvite(eveID int, unix int64) error {
	_, err := DB.Exec("INSERT OR REPLACE INTO fleet_invite_state (eve_id, last_invite_unix) VALUES (?, ?)", eveID, unix)
	return err
}

// Projects / freelance jobs watermark state

func projectsStateColumn(kind string) string {
	if kind == "project" {
		return "projects_watermark"
	}
	return "jobs_watermark"
}

// GetProjectsWatermark returns the last-processed last_modified timestamp for a
// kind ("project" or "freelance job"), or "" when the guild has no state yet.
func GetProjectsWatermark(guildID, kind string) (string, error) {
	if DB == nil {
		return "", fmt.Errorf("database not initialized")
	}
	var wm string
	err := DB.QueryRow(fmt.Sprintf("SELECT %s FROM projects_state WHERE guild_id = ?", projectsStateColumn(kind)), guildID).Scan(&wm)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return wm, nil
}

// SetProjectsWatermark stores the last-processed last_modified timestamp for a kind.
func SetProjectsWatermark(guildID, kind, watermark string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := DB.Exec(fmt.Sprintf(`
		INSERT INTO projects_state (guild_id, %s) VALUES (?, ?)
		ON CONFLICT(guild_id) DO UPDATE SET %s = excluded.%s
	`, projectsStateColumn(kind), projectsStateColumn(kind), projectsStateColumn(kind)), guildID, watermark)
	return err
}

func projectsPostedColumn(kind string) string {
	if kind == "project" {
		return "projects_posted"
	}
	return "jobs_posted"
}

// GetProjectsPostedIDs returns the IDs of already-posted finished entries for a
// kind ("project" or "freelance job"). Finished entries are deduped by ID
// instead of timestamp because expired/closed jobs may keep the last_modified
// they had while active.
func GetProjectsPostedIDs(guildID, kind string) (map[string]bool, error) {
	out := map[string]bool{}
	if DB == nil {
		return out, fmt.Errorf("database not initialized")
	}
	var raw string
	err := DB.QueryRow(fmt.Sprintf("SELECT %s FROM projects_state WHERE guild_id = ?", projectsPostedColumn(kind)), guildID).Scan(&raw)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	for _, id := range strings.Split(raw, ",") {
		if id != "" {
			out[id] = true
		}
	}
	return out, nil
}

// SetProjectsPostedIDs stores the IDs of already-posted finished entries.
func SetProjectsPostedIDs(guildID, kind string, ids map[string]bool) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	list := make([]string, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	slices.Sort(list)
	_, err := DB.Exec(fmt.Sprintf(`
		INSERT INTO projects_state (guild_id, %s) VALUES (?, ?)
		ON CONFLICT(guild_id) DO UPDATE SET %s = excluded.%s
	`, projectsPostedColumn(kind), projectsPostedColumn(kind), projectsPostedColumn(kind)), guildID, strings.Join(list, ","))
	return err
}

// SetCharacterToken stores the ESI refresh token a guild member linked for
// their own character via the dashboard, replacing any previous token.
func SetCharacterToken(eveID int, refreshToken, guildID, discordUserID string) error {
	_, err := DB.Exec("INSERT OR REPLACE INTO character_tokens (eve_character_id, refresh_token, guild_id, discord_user_id, linked_at) VALUES (?, ?, ?, ?, ?)",
		eveID, refreshToken, guildID, discordUserID, time.Now())
	return err
}

// GetGuildCharacterTokens returns the character-linked ESI refresh tokens for
// a guild, keyed by EVE character ID.
func GetGuildCharacterTokens(guildID string) (map[int]string, error) {
	rows, err := DB.Query("SELECT eve_character_id, refresh_token FROM character_tokens WHERE guild_id = ?", guildID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	tokens := make(map[int]string)
	for rows.Next() {
		var eveID int
		var token string
		if err := rows.Scan(&eveID, &token); err != nil {
			return nil, err
		}
		tokens[eveID] = token
	}
	return tokens, rows.Err()
}

type GuildDonationSettings struct {
	GuildID   string  `json:"guild_id"`
	ChannelID *string `json:"channel_id"`
}

func GetGuildDonationSettings(guildID string) (*GuildDonationSettings, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	var s GuildDonationSettings
	s.GuildID = guildID
	var ch sql.NullString
	err := DB.QueryRow("SELECT channel_id FROM GuildDonationSettings WHERE guild_id = ?", guildID).Scan(&ch)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if ch.Valid && ch.String != "" {
		s.ChannelID = &ch.String
	}
	return &s, nil
}

func SetGuildDonationChannel(guildID string, channelID *string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	if channelID == nil || *channelID == "" {
		_, err := DB.Exec("DELETE FROM GuildDonationSettings WHERE guild_id = ?", guildID)
		return err
	}
	_, err := DB.Exec(`
		INSERT INTO GuildDonationSettings (guild_id, channel_id)
		VALUES (?, ?)
		ON CONFLICT(guild_id) DO UPDATE SET channel_id = excluded.channel_id
	`, guildID, *channelID)
	return err
}

type TrackedCorporation struct {
	GuildID   string `json:"guild_id"`
	RoleID    string `json:"role_id"`
	EveCorpID int    `json:"eve_corp_id"`
}

func GetTrackedCorporation(guildID, roleID string) (*TrackedCorporation, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	var tc TrackedCorporation
	tc.GuildID = guildID
	tc.RoleID = roleID
	err := DB.QueryRow("SELECT eve_corp_id FROM TrackedCorporations WHERE guild_id = ? AND role_id = ?", guildID, roleID).Scan(&tc.EveCorpID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &tc, nil
}

func AddTrackedCorporation(guildID, roleID string, eveCorpID int) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := DB.Exec(`
		INSERT INTO TrackedCorporations (guild_id, role_id, eve_corp_id)
		VALUES (?, ?, ?)
		ON CONFLICT(guild_id, role_id) DO UPDATE SET eve_corp_id = excluded.eve_corp_id
	`, guildID, roleID, eveCorpID)
	return err
}

func RemoveTrackedCorporation(guildID, roleID string) (bool, error) {
	if DB == nil {
		return false, fmt.Errorf("database not initialized")
	}
	res, err := DB.Exec("DELETE FROM TrackedCorporations WHERE guild_id = ? AND role_id = ?", guildID, roleID)
	if err != nil {
		return false, err
	}
	rowsAffected, _ := res.RowsAffected()
	return rowsAffected > 0, nil
}

func GetTrackedCorporationsForGuild(guildID string) ([]TrackedCorporation, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	rows, err := DB.Query("SELECT role_id, eve_corp_id FROM TrackedCorporations WHERE guild_id = ?", guildID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []TrackedCorporation
	for rows.Next() {
		var tc TrackedCorporation
		tc.GuildID = guildID
		if err := rows.Scan(&tc.RoleID, &tc.EveCorpID); err == nil {
			results = append(results, tc)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func GetAllUniqueTrackedCorpIDs() ([]int, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	rows, err := DB.Query("SELECT DISTINCT eve_corp_id FROM TrackedCorporations")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var corps []int
	for rows.Next() {
		var cid int
		if err := rows.Scan(&cid); err == nil {
			corps = append(corps, cid)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return corps, nil
}

func GetGuildsTrackingCorp(eveCorpID int) ([]string, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	rows, err := DB.Query("SELECT DISTINCT guild_id FROM TrackedCorporations WHERE eve_corp_id = ?", eveCorpID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var guilds []string
	for rows.Next() {
		var gid string
		if err := rows.Scan(&gid); err == nil {
			guilds = append(guilds, gid)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return guilds, nil
}

type DonationRecord struct {
	TransactionID  int64     `json:"transaction_id"`
	ReceiverCorpID int       `json:"receiver_corp_id"`
	DonorID        int       `json:"donor_id"`
	DonorType      string    `json:"donor_type"`
	Amount         float64   `json:"amount"`
	Balance        float64   `json:"balance"`
	Date           time.Time `json:"date"`
}

type DonorLeaderboardEntry struct {
	DonorID       int     `json:"donor_id"`
	DonorType     string  `json:"donor_type"`
	TotalAmount   float64 `json:"total_amount"`
	DonationCount int     `json:"donation_count"`
}

func DonationRecordExists(transactionID int64) (bool, error) {
	if DB == nil {
		return false, fmt.Errorf("database not initialized")
	}
	var count int
	err := DB.QueryRow("SELECT COUNT(*) FROM DonationRecord WHERE transaction_id = ?", transactionID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func DonationExists(receiverCorpID, donorID int, amount, balance float64, t time.Time) (bool, error) {
	if DB == nil {
		return false, fmt.Errorf("database not initialized")
	}
	start := t.Add(-90 * time.Second)
	end := t.Add(90 * time.Second)

	var count int
	var err error
	if balance > 0 {
		err = DB.QueryRow(`
			SELECT COUNT(*) FROM DonationRecord
			WHERE receiver_corp_id = ? AND donor_id = ? AND ABS(amount - ?) < 0.01
			AND (
				(balance > 0 AND ABS(balance - ?) < 0.01)
				OR (balance == 0 AND date >= ? AND date <= ?)
			)
		`, receiverCorpID, donorID, amount, balance, start, end).Scan(&count)
	} else {
		err = DB.QueryRow(`
			SELECT COUNT(*) FROM DonationRecord
			WHERE receiver_corp_id = ? AND donor_id = ? AND ABS(amount - ?) < 0.01
			AND (date >= ? AND date <= ?)
		`, receiverCorpID, donorID, amount, start, end).Scan(&count)
	}
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func InsertDonationRecord(rec DonationRecord) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := DB.Exec(`
		INSERT INTO DonationRecord (transaction_id, receiver_corp_id, donor_id, donor_type, amount, balance, date)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(transaction_id) DO NOTHING
	`, rec.TransactionID, rec.ReceiverCorpID, rec.DonorID, rec.DonorType, rec.Amount, rec.Balance, rec.Date)
	return err
}

func GetDonationsLeaderboard(guildID string, limit int) ([]DonorLeaderboardEntry, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := DB.Query(`
		SELECT d.donor_id, d.donor_type, SUM(d.amount) AS total_amount, COUNT(*) AS donation_count
		FROM DonationRecord d
		WHERE d.receiver_corp_id IN (
			SELECT eve_corp_id FROM TrackedCorporations WHERE guild_id = ?
		)
		GROUP BY d.donor_id, d.donor_type
		ORDER BY total_amount DESC
		LIMIT ?
	`, guildID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var entries []DonorLeaderboardEntry
	for rows.Next() {
		var entry DonorLeaderboardEntry
		if err := rows.Scan(&entry.DonorID, &entry.DonorType, &entry.TotalAmount, &entry.DonationCount); err == nil {
			entries = append(entries, entry)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

