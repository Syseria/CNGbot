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

func formatConnection(trackedName string, sys systemInfo) string {
	return fmt.Sprintf("New connection to %s ⇢ %s [%s](https://zkillboard.com/system/%d/)",
		trackedName, systemDetail(sys.SystemClass, sys.Security, sys.ClassTitle), sys.Name, sys.ID)
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
	if !ok {
		// baseline only: don't flood the channel with pre-existing connections;
		// baseline from the DB's clock so lastSeen lines up with inserted_at
		pool, err := sql.Open("pgx", dsn)
		if err != nil {
			log.Printf("wanderer: opening database for guild %s: %v", guildID, err)
			return
		}
		defer pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var baseline time.Time
		if err := pool.QueryRowContext(ctx, "SELECT COALESCE(MAX(inserted_at), now()) FROM map_chain_v1").Scan(&baseline); err != nil {
			log.Printf("wanderer: reading max inserted_at for guild %s: %v", guildID, err)
			baseline = time.Now().UTC()
		}
		if err := db.SetWandererState(guildID, baseline); err != nil {
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
		SELECT c.solar_system_source, c.solar_system_target,
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
		var srcName, dstName, srcSec, dstSec, srcTitle, dstTitle string
		var srcClass, dstClass int
		var insertedAt time.Time
		if err := rows.Scan(&srcID, &dstID,
			&srcName, &srcClass, &srcSec, &srcTitle,
			&dstName, &dstClass, &dstSec, &dstTitle,
			&insertedAt); err != nil {
			log.Printf("wanderer: scanning row for guild %s: %v", guildID, err)
			continue
		}
		if insertedAt.After(maxSeen) {
			maxSeen = insertedAt
		}

		// announce each connection pair only once per guild: downtime re-adds
		// create new rows for the same pair and must not re-announce
		pairSeen, err := db.IsWandererPairSeen(guildID, srcID, dstID)
		if err != nil {
			log.Printf("wanderer: checking seen pair for guild %s: %v", guildID, err)
		}
		if pairSeen {
			continue
		}

		var trackedName string
		var far systemInfo
		switch {
		case idSet[srcID] && idSet[dstID]:
			// both endpoints tracked: one notification, report the target side
			trackedName, far = srcName, systemInfo{ID: dstID, Name: dstName, SystemClass: dstClass, Security: dstSec, ClassTitle: dstTitle}
		case idSet[srcID]:
			trackedName, far = srcName, systemInfo{ID: dstID, Name: dstName, SystemClass: dstClass, Security: dstSec, ClassTitle: dstTitle}
		case idSet[dstID]:
			trackedName, far = dstName, systemInfo{ID: srcID, Name: srcName, SystemClass: srcClass, Security: srcSec, ClassTitle: srcTitle}
		default:
			continue
		}
		if seen[far.ID] {
			continue
		}
		seen[far.ID] = true

		if err := db.MarkWandererPairSeen(guildID, srcID, dstID); err != nil {
			log.Printf("wanderer: marking pair seen for guild %s: %v", guildID, err)
		}
		log.Printf("wanderer: guild %s: connection %s <-> %s (inserted_at %s)", guildID, srcName, dstName, insertedAt.Format(time.RFC3339))

		if _, err := dg.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
			Content: formatConnection(trackedName, far),
			Flags:   discordgo.MessageFlagsSuppressEmbeds,
		}); err != nil {
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
