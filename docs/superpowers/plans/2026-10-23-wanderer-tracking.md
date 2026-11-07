# Wanderer Connection Tracking Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When an admin configures their wanderer PostgreSQL connection string on the dashboard and sets up `/channel_wanderer` + `/wanderer_track`, the bot polls the wanderer database and posts a Discord message whenever a new solar system connects to a tracked J-space system.

**Architecture:** New `wanderer/` package with a poller goroutine (mail-poller pattern) that queries wanderer's `map_chain_v1` (connections) joined with `map_solar_system_v2` (static system data) — so no static JSON downloads (anoik/SDE) are needed; wanderer's DB already materializes that data. Per-guild tracking state lives in two new SQLite tables; the DSN is a per-guild dashboard setting stored in the `config` table.

**Tech Stack:** Go 1.26, discordgo v0.29.0, database/sql + mattn/go-sqlite3 (existing), NEW dependency `github.com/jackc/pgx/v5` (stdlib driver only).

**Spec:** No separate spec doc — the design was approved in chat and is captured inline in this plan's Global Constraints and task code.

## Global Constraints

- Command names exactly: `channel_wanderer`, `wanderer_track`, `wanderer_untrack`.
- Config keys exactly: `wanderer_channel` (channel ID), `wanderer_dsn` (Postgres connection string). Both in the per-guild `config` SQLite table.
- New SQLite tables exactly: `wanderer_tracked (guild_id TEXT NOT NULL DEFAULT '', solar_system_id TEXT NOT NULL, system_name TEXT NOT NULL, PRIMARY KEY (guild_id, solar_system_id))` and `wanderer_state (guild_id TEXT NOT NULL DEFAULT '', last_seen TEXT NOT NULL, PRIMARY KEY (guild_id))`.
- Poll interval: 2 minutes (matches mail poller). First poll per guild is a baseline: record `last_seen`, post nothing.
- Poll query: connections in `map_chain_v1` with `inserted_at > last_seen` touching any tracked system (source OR target), joined to non-deleted maps and `map_solar_system_v2`.
- Announcement semantics (user-approved): both directions count; if both endpoints are tracked by the same guild, exactly one notification (report the target side); deduplicate repeated far-end systems per poll cycle.
- Message format (plain markdown, no embeds — repo has zero embeds):
  `**<system name>** — <HS|LS|NS|WH> (<security status for k-space / class title like C3 for w-space>) — connected from <tracked system> on map "<map name>" — https://zkillboard.com/system/<solar_system_id>/`
- `system_class` mapping: 7→HS, 8→LS, 9→NS, everything else (1–6, 12 Thera, 13 C13, 14–18 drifters, 25 Pochven)→WH with `class_title` as the detail.
- Postgres access ONLY via `github.com/jackc/pgx/v5/stdlib` registered as `database/sql` driver name `"pgx"`; every external DB call gets a `context.WithTimeout`.
- A poller must never block or crash on bad DSN/unreachable DB: log and continue (`log.Printf("wanderer: ...")`).
- Dashboard save PING-validates the DSN (`wanderer.ValidateDSN`) and refuses to save an unconnectable one; saving empty clears it.
- House touch-points for every new command: `Commands` array, handler switch case, `/help` string, `commands_test.go` `expectedCommands` map, `docs/commands.md`, `docs/usage.md`.
- Every task ends with `go build ./... && go test ./...` green and a git commit.

---

### Task 1: SQLite layer — tracked systems, poll state, DSN helpers

**Files:**
- Modify: `db/db.go` (guildTables list ends at line 206; db helpers live roughly lines 400–625, e.g. `SetGuildSeatConfig` at 498–510, `SetMailState`/`GetMailState` at 512–538)
- Modify: `db/db_test.go` (pattern: `TestGuildIntegrationsAndMailState`, lines 74–150)
- Modify: `db/db.go` imports (add `"time"` if not already imported)

**Interfaces:**
- Consumes: existing `guildTables []tableDef` mechanism and `DB` global.
- Produces (exact signatures later tasks call):
  - `type WandererTrackedSystem struct { GuildID string; SolarSystemID int64; SystemName string }`
  - `func AddWandererTrackedSystem(guildID string, solarSystemID int64, systemName string) error`
  - `func GetWandererTrackedByName(guildID, systemName string) (*WandererTrackedSystem, error)` — returns `(nil, nil)` when not found; matches case-insensitively via `lower()`
  - `func RemoveWandererTrackedSystem(guildID string, solarSystemID int64) error`
  - `func ListWandererTrackedSystems() ([]WandererTrackedSystem, error)`
  - `func GetWandererState(guildID string) (t time.Time, ok bool, err error)` — `last_seen` stored as RFC3339Nano
  - `func SetWandererState(guildID string, t time.Time) error`
  - `func SetGuildWandererDSN(guildID, dsn string) error` — empty dsn deletes the config row
  - `func GetGuildWandererDSN(guildID string) (string, error)` — returns `("", nil)` when unset

- [ ] **Step 1: Write the failing test** — append to `db/db_test.go`, following the `TestGuildIntegrationsAndMailState` pattern (temp dir sqlite, `createOrMigrateTables`, swap `DB`, restore via defer):

```go
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
```

Add the `"time"` import to the test file if missing.

- [ ] **Step 2: Run test to verify it fails** — Run: `go test ./db/ -run TestWandererTrackedAndState -v` — Expected: FAIL, undefined helpers.
- [ ] **Step 3: Implement** — In `db/db.go`:
  - Append to `guildTables` (before the closing `}` at line 206):
    ```go
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
    ```
  - Add the helpers from the Interfaces section (place near `SetMailState`/`GetMailState`), following the house guard style `if DB == nil { return fmt.Errorf("database not initialized") }`:
    ```go
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
    ```
- [ ] **Step 4: Run tests** — `go test ./db/... -v` — Expected: PASS (all db tests).
- [ ] **Step 5: Commit** — `git add db/db.go db/db_test.go && git commit -m "feat: wanderer tracked-system, poll-state and DSN db helpers"`

### Task 2: wanderer package — poller, formatting, DSN validation, system lookup; main.go wiring

**Files:**
- Create: `wanderer/wanderer.go`
- Create: `wanderer/wanderer_test.go`
- Modify: `go.mod` / `go.sum` (`go get github.com/jackc/pgx/v5`)
- Modify: `main.go` (poller block at lines 95–100)

**Interfaces:**
- Consumes (from Task 1): `db.ListWandererTrackedSystems`, `db.GetWandererState`, `db.SetWandererState`, `db.GetGuildWandererDSN`, `db.DB`.
- Produces (exact signatures later tasks call):
  - `func PollWanderer(dg *discordgo.Session)`
  - `func CheckWanderer(dg *discordgo.Session)`
  - `func SystemCategory(systemClass int) string` — 7→"HS", 8→"LS", 9→"NS", else "WH"
  - `func ValidateDSN(dsn string) error`
  - `func LookupSystem(dsn, name string) (solarSystemID int64, displayName string, err error)`

- [ ] **Step 1: Write the failing test** — create `wanderer/wanderer_test.go`:

```go
package wanderer

import "testing"

func TestSystemCategory(t *testing.T) {
	cases := map[int]string{
		1: "WH", 3: "WH", 6: "WH", // C1-C6
		7: "HS", 8: "LS", 9: "NS",
		12: "WH", 13: "WH", 15: "WH", // Thera, C13, drifter
		25: "WH", // Pochven
	}
	for class, want := range cases {
		if got := SystemCategory(class); got != want {
			t.Errorf("SystemCategory(%d) = %q, want %q", class, got, want)
		}
	}
}

func TestFormatConnection(t *testing.T) {
	wh := systemInfo{ID: 31001234, Name: "J100809", SystemClass: 3, Security: "-1.0", ClassTitle: "C3"}
	got := formatConnection("J111011", "Home", wh)
	want := "**J100809** — WH (C3) — connected from J111011 on map \"Home\" — https://zkillboard.com/system/31001234/"
	if got != want {
		t.Fatalf("wormhole line mismatch:\n got: %s\nwant: %s", got, want)
	}

	hs := systemInfo{ID: 30000142, Name: "Amarr", SystemClass: 7, Security: "1.0", ClassTitle: "High-sec"}
	got = formatConnection("J111011", "Home", hs)
	want = "**Amarr** — HS (1.0) — connected from J111011 on map \"Home\" — https://zkillboard.com/system/30000142/"
	if got != want {
		t.Fatalf("k-space line mismatch:\n got: %s\nwant: %s", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — `go test ./wanderer/ -v` — Expected: FAIL, undefined `systemInfo`/`formatConnection`.
- [ ] **Step 3: Implement** — Run `go get github.com/jackc/pgx/v5` first. Create `wanderer/wanderer.go`:

```go
package wanderer

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	_ "github.com/jackc/pgx/v5/stdlib"

	"evemaildiscord/db"
)

const pollInterval = 2 * time.Minute

// SystemCategory maps wanderer's map_solar_system_v2.system_class to a short
// label: 7 = HS, 8 = LS, 9 = NS; 1-6 = C1-C6 wormholes, 12 = Thera,
// 13 = C13, 14-18 = drifter, 25 = Pochven (all shown as WH).
func SystemCategory(systemClass int) string {
	switch systemClass {
	case 7:
		return "HS"
	case 8:
		return "LS"
	case 9:
		return "NS"
	default:
		return "WH"
	}
}

// systemDetail shows the security status for k-space or the class title
// (e.g. "C3") for wormhole space.
func systemDetail(systemClass int, security, classTitle string) string {
	switch SystemCategory(systemClass) {
	case "HS", "LS", "NS":
		return security
	default:
		return classTitle
	}
}

type systemInfo struct {
	ID          int64
	Name        string
	SystemClass int
	Security    string
	ClassTitle  string
}

func formatConnection(fromName, mapName string, sys systemInfo) string {
	return fmt.Sprintf("**%s** — %s (%s) — connected from %s on map %q — https://zkillboard.com/system/%d/",
		sys.Name, SystemCategory(sys.SystemClass), systemDetail(sys.SystemClass, sys.Security, sys.ClassTitle),
		fromName, mapName, sys.ID)
}

// ValidateDSN opens and pings a Postgres database so bad connection strings
// are rejected at save time instead of failing silently in the poller.
func ValidateDSN(dsn string) error {
	pool, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return pool.PingContext(ctx)
}

// LookupSystem resolves a system name (e.g. "J111011") against wanderer's
// static system table, which also powers the poller's joins.
func LookupSystem(dsn, name string) (int64, string, error) {
	pool, err := sql.Open("pgx", dsn)
	if err != nil {
		return 0, "", err
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var id int64
	var displayName string
	err = pool.QueryRowContext(ctx,
		"SELECT solar_system_id, solar_system_name FROM map_solar_system_v2 WHERE solar_system_name_lc = lower($1) LIMIT 1",
		strings.TrimSpace(name)).Scan(&id, &displayName)
	if err == sql.ErrNoRows {
		return 0, "", fmt.Errorf("system %q not found in the wanderer database", name)
	}
	if err != nil {
		return 0, "", err
	}
	return id, displayName, nil
}

func PollWanderer(dg *discordgo.Session) {
	CheckWanderer(dg)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for range ticker.C {
		CheckWanderer(dg)
	}
}

// CheckWanderer polls every guild that has a wanderer DSN, a notification
// channel and at least one tracked system, and posts messages for newly
// created connections. First run per guild only records a baseline.
func CheckWanderer(dg *discordgo.Session) {
	if dg == nil || db.DB == nil {
		return
	}
	tracked, err := db.ListWandererTrackedSystems()
	if err != nil {
		log.Printf("wanderer: listing tracked systems: %v", err)
		return
	}
	byGuild := make(map[string][]int64)
	for _, ts := range tracked {
		byGuild[ts.GuildID] = append(byGuild[ts.GuildID], ts.SolarSystemID)
	}
	for guildID, ids := range byGuild {
		checkGuild(dg, guildID, ids)
	}
}

func checkGuild(dg *discordgo.Session, guildID string, ids []int64) {
	var channelID string
	err := db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'wanderer_channel'", guildID).Scan(&channelID)
	if err == sql.ErrNoRows {
		return // no notification channel configured yet
	}
	if err != nil {
		log.Printf("wanderer: reading channel for guild %s: %v", guildID, err)
		return
	}

	dsn, err := db.GetGuildWandererDSN(guildID)
	if err != nil || dsn == "" {
		if err != nil {
			log.Printf("wanderer: reading dsn for guild %s: %v", guildID, err)
		}
		return
	}

	lastSeen, ok, err := db.GetWandererState(guildID)
	if err != nil {
		log.Printf("wanderer: reading state for guild %s: %v", guildID, err)
		return
	}
	now := time.Now().UTC()
	if !ok {
		// baseline only: don't flood the channel with pre-existing connections
		if err := db.SetWandererState(guildID, now); err != nil {
			log.Printf("wanderer: setting state for guild %s: %v", guildID, err)
		}
		return
	}

	idSet := make(map[int64]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}

	pool, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Printf("wanderer: opening database for guild %s: %v", guildID, err)
		return
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	rows, err := pool.QueryContext(ctx, `
		SELECT c.solar_system_source, c.solar_system_target, m.name,
		       src.solar_system_name, src.system_class, src.security, src.class_title,
		       dst.solar_system_name, dst.system_class, dst.security, dst.class_title,
		       c.inserted_at
		FROM map_chain_v1 c
		JOIN maps_v1 m ON m.id = c.map_id AND m.deleted = false
		JOIN map_solar_system_v2 src ON src.solar_system_id = c.solar_system_source
		JOIN map_solar_system_v2 dst ON dst.solar_system_id = c.solar_system_target
		WHERE c.inserted_at > $1
		  AND (c.solar_system_source = ANY($2) OR c.solar_system_target = ANY($2))
	`, lastSeen, ids)
	if err != nil {
		log.Printf("wanderer: querying connections for guild %s: %v", guildID, err)
		return
	}
	defer rows.Close()

	seen := make(map[int64]bool)
	maxSeen := lastSeen
	for rows.Next() {
		var srcID, dstID int64
		var mapName, srcName, dstName, srcSec, dstSec, srcTitle, dstTitle string
		var srcClass, dstClass int
		var insertedAt time.Time
		if err := rows.Scan(&srcID, &dstID, &mapName,
			&srcName, &srcClass, &srcSec, &srcTitle,
			&dstName, &dstClass, &dstSec, &dstTitle,
			&insertedAt); err != nil {
			log.Printf("wanderer: scanning row for guild %s: %v", guildID, err)
			continue
		}
		if insertedAt.After(maxSeen) {
			maxSeen = insertedAt
		}

		var from string
		var far systemInfo
		switch {
		case idSet[srcID] && idSet[dstID]:
			// both endpoints tracked: one notification, report the target side
			from, far = srcName, systemInfo{ID: dstID, Name: dstName, SystemClass: dstClass, Security: dstSec, ClassTitle: dstTitle}
		case idSet[srcID]:
			from, far = srcName, systemInfo{ID: dstID, Name: dstName, SystemClass: dstClass, Security: dstSec, ClassTitle: dstTitle}
		case idSet[dstID]:
			from, far = dstName, systemInfo{ID: srcID, Name: srcName, SystemClass: srcClass, Security: srcSec, ClassTitle: srcTitle}
		default:
			continue
		}
		if seen[far.ID] {
			continue
		}
		seen[far.ID] = true

		if _, err := dg.ChannelMessageSend(channelID, formatConnection(from, mapName, far)); err != nil {
			log.Printf("wanderer: posting to channel %s: %v", channelID, err)
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("wanderer: iterating rows for guild %s: %v", guildID, err)
		return
	}
	if err := db.SetWandererState(guildID, maxSeen); err != nil {
		log.Printf("wanderer: setting state for guild %s: %v", guildID, err)
	}
}
```

Note on `security`/`class_title` columns: `security` is TEXT in wanderer (e.g. "0.9"); `system_class` is INT — scan accordingly. If the live column types differ (e.g. numeric security), adapt the scan + `fmt` handling, not the format contract.

- [ ] **Step 4: Run tests + build** — `go test ./wanderer/ -v && go build ./...` — Expected: PASS / builds.
- [ ] **Step 5: Wire into main.go** — In the poller block (after `go fleet.PollAutoInvite(dg)` at main.go:100) add:
  ```go
  go wanderer.PollWanderer(dg)
  ```
  and add `"evemaildiscord/wanderer"` to imports.
- [ ] **Step 6: Run full suite** — `go build ./... && go test ./...` — Expected: PASS.
- [ ] **Step 7: Commit** — `git add go.mod go.sum wanderer/ main.go && git commit -m "feat: wanderer poller posting new map connections to Discord"`

### Task 3: Slash commands — /channel_wanderer, /wanderer_track, /wanderer_untrack (+ help, docs)

**Files:**
- Modify: `commands/commands.go` (Commands array: channel commands end at line 316; handler switch case `set_mail_channel, set_evemail_channel` starts at 2142; `/help` string lines 1760–1812)
- Modify: `commands/commands_test.go` (`expectedCommands` lines 11–58; option test pattern `TestDonationCommandsOptions` lines 234–279)
- Modify: `docs/commands.md`, `docs/usage.md`

**Interfaces:**
- Consumes (Task 1): `db.GetGuildWandererDSN`, `db.AddWandererTrackedSystem`, `db.GetWandererTrackedByName`, `db.RemoveWandererTrackedSystem`, `db.ExecuteOrQueue`.
- Consumes (Task 2): `wanderer.LookupSystem`.
- Produces: config key `wanderer_channel` (consumed by Task 2's poller query — keep the string identical), rows in `wanderer_tracked`.

- [ ] **Step 1: Write the failing test** — in `commands/commands_test.go`: add to `expectedCommands`:
  ```go
  		"channel_wanderer": true,
  		"wanderer_track":   true,
  		"wanderer_untrack": true,
  ```
  and add:
  ```go
  func TestWandererCommandsOptions(t *testing.T) {
  	byName := make(map[string]*discordgo.ApplicationCommand)
  	for _, cmd := range Commands {
  		byName[cmd.Name] = cmd
  	}

  	cw := byName["channel_wanderer"]
  	if cw == nil || len(cw.Options) != 1 || cw.Options[0].Type != discordgo.ApplicationCommandOptionChannel || !cw.Options[0].Required {
  		t.Fatalf("channel_wanderer options wrong: %+v", cw)
  	}

  	for _, name := range []string{"wanderer_track", "wanderer_untrack"} {
  		cmd := byName[name]
  		if cmd == nil || len(cmd.Options) != 1 || cmd.Options[0].Type != discordgo.ApplicationCommandOptionString || !cmd.Options[0].Required {
  			t.Fatalf("%s options wrong: %+v", name, cmd)
  		}
  		if cmd.Options[0].Name != "system" {
  			t.Fatalf("%s option name = %q, want system", name, cmd.Options[0].Name)
  		}
  	}
  }
  ```
- [ ] **Step 2: Run test to verify it fails** — `go test ./commands/ -run 'TestApplicationCommandsDefinitions|TestWandererCommandsOptions' -v` — Expected: FAIL (missing commands).
- [ ] **Step 3: Add command definitions** — append after the `set_mail_channel` definition block (ends line 316 in the Commands array):
  ```go
  		{
  			Name:        "channel_wanderer",
  			Description: "Set the channel where new wanderer wormhole connections will be posted",
  			Options: []*discordgo.ApplicationCommandOption{
  				{
  					Type:        discordgo.ApplicationCommandOptionChannel,
  					Name:        "channel",
  					Description: "The Discord channel for wanderer connection notifications",
  					Required:    true,
  					ChannelTypes: []discordgo.ChannelType{
  						discordgo.ChannelTypeGuildText,
  						discordgo.ChannelTypeGuildNews,
  					},
  				},
  			},
  		},
  		{
  			Name:        "wanderer_track",
  			Description: "Track a J-space system to be notified when a new system connects to it",
  			Options: []*discordgo.ApplicationCommandOption{
  				{
  					Type:        discordgo.ApplicationCommandOptionString,
  					Name:        "system",
  					Description: "System name, e.g. J111011",
  					Required:    true,
  				},
  			},
  		},
  		{
  			Name:        "wanderer_untrack",
  			Description: "Stop tracking a wanderer system",
  			Options: []*discordgo.ApplicationCommandOption{
  				{
  					Type:        discordgo.ApplicationCommandOptionString,
  					Name:        "system",
  					Description: "System name, e.g. J111011",
  					Required:    true,
  				},
  			},
  		},
  ```
- [ ] **Step 4: Add handler cases** — after the `case "set_mail_channel", "set_evemail_channel":` block (add `evemaildiscord/wanderer` to imports):
  ```go
  	case "channel_wanderer":
  		chanOpt := getOption(data.Options, "channel")
  		var chanID string
  		if chanOpt != nil {
  			if ch := chanOpt.ChannelValue(s); ch != nil {
  				chanID = ch.ID
  			} else if str, ok := chanOpt.Value.(string); ok {
  				chanID = str
  			}
  		}
  		if chanID == "" {
  			sendResponse(s, i.Interaction, "Could not resolve the channel. Please try again.")
  			return
  		}
  		db.ExecuteOrQueue(s, i.ChannelID, func() error {
  			_, err := db.DB.Exec("INSERT OR REPLACE INTO config (guild_id, key, value) VALUES (?, 'wanderer_channel', ?)", guildID, chanID)
  			if err != nil {
  				return err
  			}
  			sendResponse(s, i.Interaction, fmt.Sprintf("Wanderer connection notifications will be posted to <#%s>.", chanID))
  			return nil
  		})

  	case "wanderer_track":
  		sysOpt := getOption(data.Options, "system")
  		if sysOpt == nil {
  			sendResponse(s, i.Interaction, "Usage: /wanderer_track system:J111011")
  			return
  		}
  		systemName := strings.TrimSpace(sysOpt.StringValue())
  		if systemName == "" {
  			sendResponse(s, i.Interaction, "Usage: /wanderer_track system:J111011")
  			return
  		}
  		dsn, err := db.GetGuildWandererDSN(guildID)
  		if err != nil || dsn == "" {
  			sendResponse(s, i.Interaction, "No wanderer database configured. Set the connection string on the dashboard (Wanderer Integration) first.")
  			return
  		}
  		systemID, displayName, err := wanderer.LookupSystem(dsn, systemName)
  		if err != nil {
  			sendResponse(s, i.Interaction, err.Error())
  			return
  		}
  		existing, err := db.GetWandererTrackedByName(guildID, displayName)
  		if err != nil {
  			sendResponse(s, i.Interaction, "Error checking tracked systems.")
  			return
  		}
  		if existing != nil {
  			sendResponse(s, i.Interaction, fmt.Sprintf("%s is already being tracked.", displayName))
  			return
  		}
  		db.ExecuteOrQueue(s, i.ChannelID, func() error {
  			if err := db.AddWandererTrackedSystem(guildID, systemID, displayName); err != nil {
  				return err
  			}
  			sendResponse(s, i.Interaction, fmt.Sprintf("Now tracking **%s**. You will be notified when a new system connects to it.", displayName))
  			return nil
  		})

  	case "wanderer_untrack":
  		sysOpt := getOption(data.Options, "system")
  		if sysOpt == nil {
  			sendResponse(s, i.Interaction, "Usage: /wanderer_untrack system:J111011")
  			return
  		}
  		systemName := strings.TrimSpace(sysOpt.StringValue())
  		existing, err := db.GetWandererTrackedByName(guildID, systemName)
  		if err != nil {
  			sendResponse(s, i.Interaction, "Error looking up tracked systems.")
  			return
  		}
  		if existing == nil {
  			sendResponse(s, i.Interaction, fmt.Sprintf("%s is not being tracked.", systemName))
  			return
  		}
  		db.ExecuteOrQueue(s, i.ChannelID, func() error {
  			if err := db.RemoveWandererTrackedSystem(guildID, existing.SolarSystemID); err != nil {
  				return err
  			}
  			sendResponse(s, i.Interaction, fmt.Sprintf("Stopped tracking **%s**.", existing.SystemName))
  			return nil
  		})
  ```
  (All three land behind the existing admin-permission gate automatically.)
- [ ] **Step 5: Add help text** — in the `/help` string, after the `/import_donations` line (line 1804), insert:
  ```go
  			"`/channel_wanderer <channel>` - Set the channel where new wanderer wormhole connections will be posted.\n" +
  			"`/wanderer_track <system>` - Track a J-space system (e.g. J111011) to be notified when a new system connects to it.\n" +
  			"`/wanderer_untrack <system>` - Stop tracking a wanderer system.\n" +
  ```
- [ ] **Step 6: Docs** — `docs/commands.md`, after the donations block:
  ```markdown
  * `/channel_wanderer`: Set the channel where notifications about new wanderer wormhole connections will be posted.
  * `/wanderer_track`: Track a J-space system (e.g. J111011) so the bot posts when a new system connects to it in your wanderer maps.
  * `/wanderer_untrack`: Stop tracking a wanderer system.
  ```
  `docs/usage.md` — new section (place after EVE Mail Forwarding):
  ```markdown
  ## Wanderer Connection Tracking

  The bot can watch your [wanderer](https://github.com/wanderer-industries/wanderer) maps and post a message whenever a new solar system connects to a tracked J-space system. First set your wanderer PostgreSQL connection string in the dashboard's Wanderer Integration card, then:
  Helpful commands include:
  * `/channel_wanderer` to designate the channel for notifications.
  * `/wanderer_track` to start tracking a system (e.g. J111011).
  * `/wanderer_untrack` to stop tracking a system.
  ```
- [ ] **Step 7: Run tests + full suite** — `go test ./commands/ -v && go build ./... && go test ./...` — Expected: PASS.
- [ ] **Step 8: Commit** — `git add commands/commands.go commands/commands_test.go docs/commands.md docs/usage.md && git commit -m "feat: add /channel_wanderer, /wanderer_track and /wanderer_untrack commands"`

### Task 4: Dashboard — Wanderer Integration card with DSN save + ping validation

**Files:**
- Modify: `web/server.go` (routes at lines 12–31)
- Modify: `web/handlers.go` (add `HandleGuildWandererSave` mirroring `HandleGuildSeatSave` at 650–691; extend `HandleGuildConfig` confData at 338–356)
- Modify: `web/templates.go` (`GuildConfigData.Config` struct, lines 43–51)
- Modify: `web/templates/guild_config.html` (SeAT card at 107–139; flash rendering at 41–55)

**Interfaces:**
- Consumes (Task 1): `db.SetGuildWandererDSN`.
- Consumes (Task 2): `wanderer.ValidateDSN`.

- [ ] **Step 1: Route** — in `web/server.go`, after the seat route:
  ```go
  	mux.HandleFunc("/dashboard/guild/wanderer", handler.HandleGuildWandererSave)
  ```
- [ ] **Step 2: Handler** — in `web/handlers.go` (add `"evemaildiscord/wanderer"` import):
  ```go
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
  ```
  Check how the template renders success/error codes (guild_config.html:41–55). If codes are enumerated, add `wanderer_saved` with text like "Wanderer settings saved."; if it prints raw codes with underscores, follow that house behavior instead.
- [ ] **Step 3: Render read-back** — `web/templates.go`: add `WandererDSN string` to `GuildConfigData.Config`. In `HandleGuildConfig`'s config block (handlers.go:348–356) add:
  ```go
  		_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'wanderer_dsn'", guildID).Scan(&confData.WandererDSN)
  ```
  and add `WandererDSN string` to the anonymous confData struct at 338–346.
- [ ] **Step 4: Template card** — insert after the SeAT card's closing `</div>` (line 139), before Automation Settings:
  ```html
  		<!-- Wanderer Integration -->
  		<div class="bg-gray-800 border border-gray-700 rounded-lg p-5 md:p-6 mb-6 shadow-md">
  			<div class="flex flex-col md:flex-row justify-between md:items-start gap-4 mb-4">
  				<div>
  					<h2 class="text-xl font-bold text-white">Wanderer Integration (Optional)</h2>
  					<p class="text-gray-400 mt-1 text-sm md:text-base">Post notifications when new solar systems connect to tracked J-space systems on your wanderer maps. Configure with the /channel_wanderer and /wanderer_track commands.</p>
  				</div>
  				<div class="shrink-0">
  					{{if .Config.WandererDSN}}
  						<span class="inline-block px-2 py-1 text-xs font-semibold rounded bg-green-900/30 text-green-400 border border-green-800/50 uppercase tracking-wide">Configured</span>
  					{{else}}
  						<span class="inline-block px-2 py-1 text-xs font-semibold rounded bg-yellow-900/30 text-yellow-500 border border-yellow-800/50 uppercase tracking-wide">Optional</span>
  					{{end}}
  				</div>
  			</div>

  			<form action="/dashboard/guild/wanderer" method="POST" class="mt-4">
  				<input type="hidden" name="guild_id" value="{{.Guild.ID}}">
  				<div class="mb-5">
  					<label for="wanderer_dsn" class="block font-semibold mb-2 text-white text-sm">Wanderer PostgreSQL Connection String</label>
  					<input type="password" id="wanderer_dsn" name="wanderer_dsn" class="w-full px-4 py-2 bg-gray-900 border border-gray-700 rounded-md text-white text-sm focus:outline-none focus:border-indigo-500 transition-colors" placeholder="postgres://user:password@host:5432/wanderer" value="{{.Config.WandererDSN}}">
  					<p class="text-gray-500 text-xs mt-1">The bot queries the wanderer database directly to detect new map connections. Save empty to remove.</p>
  				</div>

  				<button type="submit" class="w-full sm:w-auto bg-indigo-600 hover:bg-indigo-700 text-white font-semibold py-2 px-6 rounded-md transition duration-200 ease-in-out shadow-sm">Save Wanderer Settings</button>
  			</form>
  		</div>
  ```
  Heading intentionally unnumbered to avoid renumbering the existing cards.
- [ ] **Step 5: Verify** — `go build ./... && go vet ./web/... && go test ./...` — Expected: PASS. (No web test suite exists; this task is plumbing mirroring the SeAT handler.)
- [ ] **Step 6: Commit** — `git add web/server.go web/handlers.go web/templates.go web/templates/guild_config.html && git commit -m "feat: wanderer DSN setting on guild dashboard"`