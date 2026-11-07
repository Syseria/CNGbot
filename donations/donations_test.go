package donations

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"evemaildiscord/db"

	"github.com/bwmarrin/discordgo"
	_ "github.com/mattn/go-sqlite3"
)

func setupTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_donations_pkg.db")

	database, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}

	_, err = database.Exec(`
		CREATE TABLE character_to_discord (
			eve_id INTEGER PRIMARY KEY,
			discord_id TEXT
		);
		CREATE TABLE corp_to_role (
			guild_id TEXT NOT NULL DEFAULT '',
			corp_id INTEGER NOT NULL,
			role_id TEXT NOT NULL,
			PRIMARY KEY (guild_id, corp_id)
		);
		CREATE TABLE GuildDonationSettings (
			guild_id TEXT PRIMARY KEY,
			channel_id TEXT
		);
		CREATE TABLE TrackedCorporations (
			guild_id TEXT NOT NULL,
			role_id TEXT NOT NULL,
			eve_corp_id INTEGER NOT NULL,
			PRIMARY KEY (guild_id, role_id)
		);
		CREATE TABLE DonationRecord (
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
		database.Close()
		t.Fatalf("failed to create test tables: %v", err)
	}

	oldDB := db.DB
	db.DB = database

	cleanup := func() {
		db.DB = oldDB
		_ = database.Close()
	}
	return database, cleanup
}

func TestFormatISK(t *testing.T) {
	tests := []struct {
		amount   float64
		expected string
	}{
		{amount: 0, expected: "0 ISK"},
		{amount: 500, expected: "500 ISK"},
		{amount: 1000, expected: "1,000 ISK"},
		{amount: 1000000, expected: "1,000,000 ISK"},
		{amount: 1000000000, expected: "1,000,000,000 ISK"},
		{amount: 1234567.89, expected: "1,234,567.89 ISK"},
	}

	for _, tc := range tests {
		res := FormatISK(tc.amount)
		if res != tc.expected {
			t.Errorf("FormatISK(%f) = %q; want %q", tc.amount, res, tc.expected)
		}
		if strings.Contains(res, "-") {
			t.Errorf("FormatISK(%f) contains dash: %q", tc.amount, res)
		}
	}
}

func TestFormattingRules_NoDashes_NoEmojis(t *testing.T) {
	dirtyText := "Hello-World! 🚀 Top-50 – Donors — Test 😄"
	clean := SanitizeText(dirtyText)

	if strings.Contains(clean, "-") || strings.Contains(clean, "—") || strings.Contains(clean, "–") {
		t.Errorf("SanitizeText failed to remove dashes: %q", clean)
	}
	if strings.Contains(clean, "🚀") || strings.Contains(clean, "😄") {
		t.Errorf("SanitizeText failed to remove emojis: %q", clean)
	}
}

func TestResolveEntity(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	guildID := "guild_test"

	// 1. Mapped character -> <@USER_ID>
	_, err := database.Exec("INSERT INTO character_to_discord (eve_id, discord_id) VALUES (?, ?)", 1001, "user_discord_1")
	if err != nil {
		t.Fatalf("failed to insert character_to_discord: %v", err)
	}

	res := ResolveEntity(guildID, 1001, "character")
	if res != "<@user_discord_1>" {
		t.Errorf("expected <@user_discord_1>, got %q", res)
	}

	// 2. Unmapped character -> [Character Name](https://evewho.com/character/CHARACTER_ID)
	SetCachedEntity(1002, "Bob Hunter", "character")
	res = ResolveEntity(guildID, 1002, "character")
	expected := "[Bob Hunter](https://evewho.com/character/1002)"
	if res != expected {
		t.Errorf("expected %q, got %q", expected, res)
	}

	// Unmapped character with hyphen in name gets sanitized
	SetCachedEntity(1003, "Jean-Luc Picard", "character")
	res = ResolveEntity(guildID, 1003, "character")
	if strings.Contains(res, "-") {
		t.Errorf("expected name without dash, got %q", res)
	}
	expected = "[Jean Luc Picard](https://evewho.com/character/1003)"
	if res != expected {
		t.Errorf("expected %q, got %q", expected, res)
	}

	// 3. Mapped corporation -> <@&ROLE_ID>
	_, err = database.Exec("INSERT INTO corp_to_role (guild_id, corp_id, role_id) VALUES (?, ?, ?)", guildID, 2001, "role_discord_corp")
	if err != nil {
		t.Fatalf("failed to insert corp_to_role: %v", err)
	}

	res = ResolveEntity(guildID, 2001, "corporation")
	if res != "<@&role_discord_corp>" {
		t.Errorf("expected <@&role_discord_corp>, got %q", res)
	}

	// 4. Unmapped corporation -> [Corporation Name](https://evewho.com/corporation/CORP_ID)
	SetCachedEntity(2002, "Mega-Corp Industries", "corporation")
	res = ResolveEntity(guildID, 2002, "corporation")
	if strings.Contains(res, "-") {
		t.Errorf("expected corp name without dash, got %q", res)
	}
	expected = "[Mega Corp Industries](https://evewho.com/corporation/2002)"
	if res != expected {
		t.Errorf("expected %q, got %q", expected, res)
	}

	// 5. Test GetTrackedCorpDisplayName
	// 5a. Via ESI resolution: 98818601 -> "Cult of Magik"
	dispESI := GetTrackedCorpDisplayName(guildID, "role_any", 98818601)
	if dispESI != "Cult of Magik" {
		t.Errorf("expected 'Cult of Magik' via ESI, got %q", dispESI)
	}

	// 5b. Via role name fallback when ESI has no name:
	mockSession := &discordgo.Session{
		State: discordgo.NewState(),
	}
	guild := &discordgo.Guild{
		ID: guildID,
		Roles: []*discordgo.Role{
			{ID: "role_discord_fallback", Name: "Magik Vanguard"},
		},
	}
	_ = mockSession.State.GuildAdd(guild)

	dispRole := GetTrackedCorpDisplayName(guildID, "role_discord_fallback", 999999999, mockSession)
	if dispRole != "Magik Vanguard" {
		t.Errorf("expected 'Magik Vanguard' via role name fallback, got %q", dispRole)
	}
}

func TestFormatDonationAnnouncement(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	guildID := "guild_announcement"
	_, _ = database.Exec("INSERT INTO character_to_discord (eve_id, discord_id) VALUES (?, ?)", 1001, "discord_donor_1")

	msg := FormatDonationAnnouncement(guildID, 1001, "character", 500000000)
	expected := "Thank you to <@discord_donor_1> for the generous donation of 500,000,000 ISK!"
	if msg != expected {
		t.Errorf("expected %q, got %q", expected, msg)
	}

	if strings.Contains(msg, "-") {
		t.Errorf("announcement contains dash: %q", msg)
	}
}

func TestBannedDonation(t *testing.T) {
	tests := []struct {
		name     string
		reason   string
		expected bool
	}{
		{"Substring match", "[BPC] Drake Navy Issue", true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := IsDonationBanned(test.reason)
			if result != test.expected {
				t.Errorf("expected %v, got %v", test.expected, result)
			}
		})
	}
}

func TestBuildLeaderboard(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	guildID := "guild_lb"
	corpID := 98765

	// Track corporation
	_, err := database.Exec("INSERT INTO TrackedCorporations (guild_id, role_id, eve_corp_id) VALUES (?, ?, ?)", guildID, "role_c1", corpID)
	if err != nil {
		t.Fatalf("failed to insert tracked corp: %v", err)
	}
	SetCachedEntity(corpID, "Test Corporation Alpha", "corporation")

	// Empty leaderboard
	lb, err := BuildLeaderboard(guildID)
	if err != nil {
		t.Fatalf("BuildLeaderboard failed: %v", err)
	}
	if !strings.Contains(lb, "Top 50 Donors All Time for Test Corporation Alpha") || !strings.Contains(lb, "No donations recorded yet") {
		t.Errorf("unexpected empty leaderboard: %q", lb)
	}
	if strings.Contains(lb, "<@&role_c1>") {
		t.Errorf("leaderboard title should not ping role: %q", lb)
	}
	if strings.Contains(lb, "-") {
		t.Errorf("leaderboard contains dash: %q", lb)
	}

	// Populate donations
	_, _ = database.Exec("INSERT INTO character_to_discord (eve_id, discord_id) VALUES (?, ?)", 1001, "user1")
	SetCachedEntity(1002, "Alice Trader", "character")

	now := time.Now()
	_, _ = database.Exec("INSERT INTO DonationRecord (transaction_id, receiver_corp_id, donor_id, donor_type, amount, date) VALUES (?, ?, ?, ?, ?, ?)", 1, corpID, 1001, "character", 1000000000, now)
	_, _ = database.Exec("INSERT INTO DonationRecord (transaction_id, receiver_corp_id, donor_id, donor_type, amount, date) VALUES (?, ?, ?, ?, ?, ?)", 2, corpID, 1001, "character", 500000000, now)
	_, _ = database.Exec("INSERT INTO DonationRecord (transaction_id, receiver_corp_id, donor_id, donor_type, amount, date) VALUES (?, ?, ?, ?, ?, ?)", 3, corpID, 1002, "character", 2000000000, now)

	lb, err = BuildLeaderboard(guildID)
	if err != nil {
		t.Fatalf("BuildLeaderboard failed: %v", err)
	}

	if strings.Contains(lb, "-") {
		t.Errorf("leaderboard contains dash: %q", lb)
	}

	lines := strings.Split(strings.TrimSpace(lb), "\n")
	// Header + blank line + 2 entries
	if len(lines) < 3 {
		t.Fatalf("unexpected line count: %d (%q)", len(lines), lb)
	}

	// Rank 1 must be Alice Trader with 2,000,000,000 ISK (1 donation)
	if !strings.Contains(lines[len(lines)-2], "Alice Trader") || !strings.Contains(lines[len(lines)-2], "2,000,000,000 ISK") || !strings.Contains(lines[len(lines)-2], "(1 donation)") {
		t.Errorf("unexpected rank 1: %q", lines[len(lines)-2])
	}

	// Rank 2 must be <@user1> with 1,500,000,000 ISK (2 donations)
	if !strings.Contains(lines[len(lines)-1], "<@user1>") || !strings.Contains(lines[len(lines)-1], "1,500,000,000 ISK") || !strings.Contains(lines[len(lines)-1], "(2 donations)") {
		t.Errorf("unexpected rank 2: %q", lines[len(lines)-1])
	}
}

func TestFetchCorpWalletJournal(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/corporations/999/wallets/1/journal/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `[
			{
				"id": 10001,
				"date": "2026-03-01T12:00:00Z",
				"ref_type": "player_donation",
				"first_party_id": 2111,
				"second_party_id": 999,
				"amount": 50000000.0
			},
			{
				"id": 10002,
				"date": "2026-03-01T11:00:00Z",
				"ref_type": "market_escrow",
				"first_party_id": 2112,
				"second_party_id": 999,
				"amount": 10000.0
			}
		]`)
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	client := server.Client()
	client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = server.Listener.Addr().String()
		return http.DefaultTransport.RoundTrip(req)
	})

	entries, err := FetchCorpWalletJournal(client, 999, 1)
	if err != nil {
		t.Fatalf("FetchCorpWalletJournal failed: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].ID != 10001 || entries[0].RefType != "player_donation" || entries[0].Amount != 50000000 {
		t.Errorf("unexpected entry 0: %+v", entries[0])
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

const (
	testPaste1 = `2026.09.05 20:05	Player Donation	500.000.000 ISK	7.782.693.407 ISK	Amayah Thara deposited cash into Cult of Magik's account2026.09.04 20:31	Player Donation	408.002.526 ISK	8.490.778.267 ISK	[r] Talion Starzise deposited cash into Cult of Magik's account2026.09.04 19:53	Player Donation	1.500.000.000 ISK	8.082.540.615 ISK	[r] Fireburner20091 deposited cash into Cult of Magik's account2026.09.04 12:19	Player Donation	1.500.000.000 ISK	6.521.698.438 ISK	[r] MonkeycheeseUK Koskanaiken deposited cash into Cult of Magik's account2026.09.04 00:58	Player Donation	1.500.000.000 ISK	4.992.346.174 ISK	[r] Alfony VIII deposited cash into Cult of Magik's account2026.09.04 00:56	Player Donation	2.000.000.000 ISK	3.492.321.174 ISK	animal ears deposited cash into Cult of Magik's account2026.09.04 00:34	Player Donation	0 ISK	1.492.321.174 ISK	Mye Esubria deposited cash into Cult of Magik's account2026.09.03 09:25	Player Donation	25.652 ISK	2.125.896.305 ISK	[r] Talion Starzise deposited cash into Cult of Magik's account2026.09.01 02:05	Player Donation	2.000.000.000 ISK	9.221.178.592 ISK	Sharez Mordon deposited cash into Cult of Magik's account2026.08.31 22:56	Player Donation	2.000.000.000 ISK	7.220.471.043 ISK	drizzt D'Orden deposited cash into Cult of Magik's account2026.08.30 22:47	Player Donation	2.000.000.000 ISK	15.433.059.025 ISK	Tarkadoll deposited cash into Cult of Magik's account2026.08.30 22:47	Player Donation	1.000.000.000 ISK	13.433.059.025 ISK	[r] Elayn Wolfsblut deposited cash into Cult of Magik's account2026.08.30 19:39	Player Donation	10.000.000 ISK	14.476.057.835 ISK	[r] Trash Bin29 deposited cash into Cult of Magik's account2026.08.30 18:02	Player Donation	400.000.000 ISK	14.465.982.835 ISK	demongateuk deposited cash into Cult of Magik's account2026.08.23 22:37	Player Donation	750.000.000 ISK	31.828.530.038 ISK	drizzt D'Orden deposited cash into Cult of Magik's account2026.08.20 01:19	Player Donation	300.000.000 ISK	31.368.530.038 ISK	demongateuk deposited cash into Cult of Magik's account2026.08.19 01:32	Player Donation	1.000.000.000 ISK	31.460.826.476 ISK	Mobiggins Cooper deposited cash into Cult of Magik's account2026.08.18 13:22	Player Donation	600.248.320 ISK	30.860.826.476 ISK	Talion Starzise deposited cash into Cult of Magik's account2026.08.17 17:15	Player Donation	300.000.000 ISK	30.743.578.408 ISK	demongateuk deposited cash into Cult of Magik's account2026.08.17 16:55	Player Donation	300.000.000 ISK	30.761.149.988 ISK	demongateuk deposited cash into Cult of Magik's account2026.08.12 21:53	Player Donation	20.000.000.000 ISK	33.012.733.839 ISK	Talion Starzise deposited cash into Cult of Magik's account2026.08.11 12:24	Player Donation	800.000.000 ISK	13.280.274.128 ISK	spark vold deposited cash into Cult of Magik's account2026.08.09 16:21	Player Donation	160.000.000 ISK	12.431.820.673 ISK	spark vold deposited cash into Cult of Magik's account`

	testPaste2 = `2026.08.31 22:56	Player Donation	2.000.000.000 ISK	7.220.471.043 ISK	drizzt D'Orden deposited cash into Cult of Magik's account2026.08.30 22:47	Player Donation	2.000.000.000 ISK	15.433.059.025 ISK	Tarkadoll deposited cash into Cult of Magik's account2026.08.30 22:47	Player Donation	1.000.000.000 ISK	13.433.059.025 ISK	[r] Elayn Wolfsblut deposited cash into Cult of Magik's account2026.08.30 19:39	Player Donation	10.000.000 ISK	14.476.057.835 ISK	[r] Trash Bin29 deposited cash into Cult of Magik's account2026.08.30 18:02	Player Donation	400.000.000 ISK	14.465.982.835 ISK	demongateuk deposited cash into Cult of Magik's account2026.08.23 22:37	Player Donation	750.000.000 ISK	31.828.530.038 ISK	drizzt D'Orden deposited cash into Cult of Magik's account2026.08.20 01:19	Player Donation	300.000.000 ISK	31.368.530.038 ISK	demongateuk deposited cash into Cult of Magik's account2026.08.19 01:32	Player Donation	1.000.000.000 ISK	31.460.826.476 ISK	Mobiggins Cooper deposited cash into Cult of Magik's account2026.08.18 13:22	Player Donation	600.248.320 ISK	30.860.826.476 ISK	Talion Starzise deposited cash into Cult of Magik's account2026.08.17 17:15	Player Donation	300.000.000 ISK	30.743.578.408 ISK	demongateuk deposited cash into Cult of Magik's account2026.08.17 16:55	Player Donation	300.000.000 ISK	30.761.149.988 ISK	demongateuk deposited cash into Cult of Magik's account2026.08.12 21:53	Player Donation	20.000.000.000 ISK	33.012.733.839 ISK	Talion Starzise deposited cash into Cult of Magik's account2026.08.11 12:24	Player Donation	800.000.000 ISK	13.280.274.128 ISK	spark vold deposited cash into Cult of Magik's account2026.08.09 16:21	Player Donation	160.000.000 ISK	12.431.820.673 ISK	spark vold deposited cash into Cult of Magik's account2026.08.06 19:25	Player Donation	2.000.000.000 ISK	11.188.944.941 ISK	[r] Khromius deposited cash into Cult of Magik's account2026.08.03 17:49	Player Donation	100.000.000 ISK	6.163.152.674 ISK	[r] elezee deposited cash into Cult of Magik's account2026.08.02 12:44	Player Donation	10.000.000 ISK	6.332.756.205 ISK	EVIL REAVER deposited cash into Cult of Magik's account2026.08.02 10:43	Player Donation	50.000.000 ISK	6.320.861.211 ISK	daltanion Deudigren deposited cash into Cult of Magik's account2026.07.31 11:55	Player Donation	100.000.000 ISK	6.220.458.078 ISK	[r] elezee deposited cash into Cult of Magik's account`

	testPaste3 = `2026.07.31 11:55	Player Donation	100.000.000 ISK	6.220.458.078 ISK	[r] elezee deposited cash into Cult of Magik's account2026.07.30 21:18	Player Donation	300.000.000 ISK	6.109.895.281 ISK	demongateuk deposited cash into Cult of Magik's account2026.07.30 20:45	Player Donation	300.000.000 ISK	8.309.107.618 ISK	Khelanna deposited cash into Cult of Magik's account2026.07.30 20:44	Player Donation	300.000.000 ISK	8.008.598.705 ISK	[r] Orin Mahyisti deposited cash into Cult of Magik's account2026.07.25 15:57	Player Donation	100.000.000 ISK	7.911.563.308 ISK	demongateuk deposited cash into Cult of Magik's account2026.07.21 04:59	Player Donation	200.000.000 ISK	7.300.653.525 ISK	[r] Karkax deposited cash into Cult of Magik's account2026.07.15 18:44	Player Donation	200.000.000 ISK	14.187.043.416 ISK	[r] Stuza deposited cash into Cult of Magik's account2026.07.15 17:53	Player Donation	200.000.000 ISK	13.986.555.208 ISK	Rick Pounder deposited cash into Cult of Magik's account2026.07.14 17:26	Player Donation	40.000.000 ISK	15.647.426.128 ISK	[r] Shirley Ellingworth deposited cash into Cult of Magik's account2026.07.07 21:47	Player Donation	20.000.000 ISK	16.006.755.569 ISK	demongateuk deposited cash into Cult of Magik's account2026.07.06 21:41	Player Donation	300.000.000 ISK	16.000.973.468 ISK	demongateuk deposited cash into Cult of Magik's account2026.07.04 07:57	Player Donation	1.000.000.000 ISK	16.471.936.910 ISK	[r] Andre Veric deposited cash into Cult of Magik's account2026.07.04 01:06	Player Donation	1.000.000.000 ISK	19.971.487.535 ISK	[r] Wes Magyar deposited cash into Cult of Magik's account`

	testPaste4 = `2026.06.30 15:06	Player Donation	5.000.000.000 ISK	22.508.515.184 ISK	[r] Talion Starzise deposited cash into Cult of Magik's account2026.06.29 14:32	Player Donation	400.000.000 ISK	17.861.242.369 ISK	Rick Pounder deposited cash into Cult of Magik's account2026.06.21 03:47	Player Donation	1.000.000.000 ISK	21.859.487.148 ISK	[r] Rojori Jaynara deposited cash into Cult of Magik's account2026.06.19 21:03	Player Donation	500.000.000 ISK	20.856.090.934 ISK	[r] Rojori Jaynara deposited cash into Cult of Magik's account2026.06.10 23:35	Player Donation	250.000.000 ISK	20.821.481.540 ISK	demongateuk deposited cash into Cult of Magik's account2026.06.09 22:59	Player Donation	1.000.000.000 ISK	20.570.444.393 ISK	[r] Orin Mahyisti deposited cash into Cult of Magik's account2026.06.05 07:19	Player Donation	4.999.999.999 ISK	20.000.000.000 ISK	[r] Talion Starzise deposited cash into Cult of Magik's account2026.06.05 07:18	Player Donation	280.759.819 ISK	15.000.000.001 ISK	Talion Starzise deposited cash into Cult of Magik's account2026.06.05 07:16	Player Donation	2.719.240.180 ISK	14.719.240.181 ISK	[r] Talion Starzise deposited cash into Cult of Magik's account2026.06.05 07:10	Player Donation	577.376.895 ISK	12.000.000.000 ISK	[r] Talion Starzise deposited cash into Cult of Magik's account2026.06.01 19:17	Player Donation	1.000.000 ISK	11.416.359.258 ISK	[r] Nardrus Malto deposited cash into Cult of Magik's account`
)

func TestParseWalletJournalText_AllPastes(t *testing.T) {
	e1 := ParseWalletJournalText(testPaste1)
	if len(e1) != 23 {
		t.Fatalf("expected 23 entries from paste 1, got %d", len(e1))
	}
	if e1[0].DonorName != "Amayah Thara" || e1[0].Amount != 500000000.0 || e1[0].CorpName != "Cult of Magik" {
		t.Errorf("unexpected entry 0: %+v", e1[0])
	}
	if e1[1].DonorName != "Talion Starzise" || e1[1].Amount != 408002526.0 {
		t.Errorf("unexpected entry 1: %+v", e1[1])
	}

	e2 := ParseWalletJournalText(testPaste2)
	if len(e2) != 19 {
		t.Fatalf("expected 19 entries from paste 2, got %d", len(e2))
	}

	e3 := ParseWalletJournalText(testPaste3)
	if len(e3) != 13 {
		t.Fatalf("expected 13 entries from paste 3, got %d", len(e3))
	}

	e4 := ParseWalletJournalText(testPaste4)
	if len(e4) != 11 {
		t.Fatalf("expected 11 entries from paste 4, got %d", len(e4))
	}
}

func TestImportWalletJournal_Deduplication(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	guildID := "guild_import_test"
	corpID := 98765432

	if err := db.AddTrackedCorporation(guildID, "role_cult", corpID); err != nil {
		t.Fatalf("failed to add tracked corp: %v", err)
	}
	SetCachedEntity(corpID, "Cult of Magik", "corporation")

	// 1. Import Paste 1 (23 entries, 1 is 0 ISK, so 22 positive donations imported)
	imported1, dups1, corpName, err := ImportWalletJournal(guildID, corpID, testPaste1)
	if err != nil {
		t.Fatalf("Import 1 failed: %v", err)
	}
	if imported1 != 22 || dups1 != 0 {
		t.Errorf("import 1: got %d imported, %d dups; want 22 imported, 0 dups", imported1, dups1)
	}
	if corpName != "Cult of Magik" {
		t.Errorf("corpName = %q; want 'Cult of Magik'", corpName)
	}

	// Re-importing Paste 1 must result in 0 imported, 22 duplicates
	reImport1, reDups1, _, err := ImportWalletJournal(guildID, corpID, testPaste1)
	if err != nil {
		t.Fatalf("Re-import 1 failed: %v", err)
	}
	if reImport1 != 0 || reDups1 != 22 {
		t.Errorf("re-import 1: got %d imported, %d dups; want 0 imported, 22 dups", reImport1, reDups1)
	}

	// 2. Import Paste 2 (August 2026). Overlaps heavily with Paste 1 (last 30 days).
	// 19 total entries. 14 overlap with Paste 1, 5 are new (Aug 6, Aug 3, Aug 2, Aug 2, July 31).
	imported2, dups2, _, err := ImportWalletJournal(guildID, corpID, testPaste2)
	if err != nil {
		t.Fatalf("Import 2 failed: %v", err)
	}
	if imported2 != 5 || dups2 != 14 {
		t.Errorf("import 2: got %d imported, %d dups; want 5 imported, 14 dups", imported2, dups2)
	}

	// 3. Import Paste 3 (July 2026). Overlaps on July 31 with Paste 2.
	// 13 total entries. 1 overlaps with Paste 2, 12 are new.
	imported3, dups3, _, err := ImportWalletJournal(guildID, corpID, testPaste3)
	if err != nil {
		t.Fatalf("Import 3 failed: %v", err)
	}
	if imported3 != 12 || dups3 != 1 {
		t.Errorf("import 3: got %d imported, %d dups; want 12 imported, 1 dups", imported3, dups3)
	}

	// 4. Import Paste 4 (June 2026). 11 entries, all new.
	imported4, dups4, _, err := ImportWalletJournal(guildID, corpID, testPaste4)
	if err != nil {
		t.Fatalf("Import 4 failed: %v", err)
	}
	if imported4 != 11 || dups4 != 0 {
		t.Errorf("import 4: got %d imported, %d dups; want 11 imported, 0 dups", imported4, dups4)
	}

	// Total unique donations: 22 + 5 + 12 + 11 = 50 donations!
	var totalDonations int
	err = database.QueryRow("SELECT COUNT(*) FROM DonationRecord WHERE receiver_corp_id = ?", corpID).Scan(&totalDonations)
	if err != nil {
		t.Fatalf("failed to count records: %v", err)
	}
	if totalDonations != 50 {
		t.Errorf("expected exactly 50 total records in database, got %d", totalDonations)
	}

	// Leaderboard check
	lb, err := BuildLeaderboard(guildID)
	if err != nil {
		t.Fatalf("BuildLeaderboard failed: %v", err)
	}
	if !strings.Contains(lb, "Top 50 Donors All Time") {
		t.Errorf("leaderboard missing title: %q", lb)
	}
	if strings.Contains(lb, "-") {
		t.Errorf("leaderboard contains dash: %q", lb)
	}
	// Verify top donors like Talion Starzise
	if !strings.Contains(lb, "Talion Starzise") {
		t.Errorf("leaderboard missing top donor Talion Starzise: %q", lb)
	}
}
