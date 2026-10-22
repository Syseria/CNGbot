package commands

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestApplicationCommandsDefinitions(t *testing.T) {
	expectedCommands := map[string]bool{
		"dashboard":              true,
		"map_admin":              true,
		"map_char_id":            true,
		"map_corp_id":            true,
		"map_corp":               true,
		"map_alliance":           true,
		"map_greet_role":        true,
		"seat_needed_roles":     true,
		"req_corp_for_alliance": true,
		"rm_corp":               true,
		"rm_corp_id":             true,
		"rm_alliance":            true,
		"rm_char_id":             true,
		"map_char":               true,
		"sync":                   true,
		"report":                 true,
		"help":                   true,
		"exclude_sync":           true,
		"exclude_map":            true,
		"exclude_corp_standing":  true,
		"exclude_2fa":            true,
		"toggle_2fa":              true,
		"set_log_channel":        true,
		"set_events_channel":     true,
		"set_mail_channel":       true,
		"toggle_logs":            true,
		"toggle_automap":         true,
		"toggle_auto_fleet_invite": true,
		"set_fleet_alliance":      true,
		"link":                    true,
		"courier_fee":             true,
		"set_greeting":           true,
		"set_guest":              true,
		"set_missing_seat_role":  true,
		"map_standing_terrible":  true,
		"map_standing_bad":       true,
		"map_standing_neutral":   true,
		"map_standing_good":      true,
		"map_standing_excellent": true,
		"char_info":                       true,
		"id":                              true,
		"set_donations_channel":           true,
		"set_donations_track_corporation": true,
		"donations_leaderboard":           true,
		"post_donations_leaderboard":      true,
		"import_donations":                true,
	}

	if len(Commands) != len(expectedCommands) {
		t.Fatalf("expected %d commands, got %d", len(expectedCommands), len(Commands))
	}

	foundCommands := make(map[string]bool)

	for _, cmd := range Commands {
		if !expectedCommands[cmd.Name] {
			t.Errorf("unexpected command name: %s", cmd.Name)
		}
		foundCommands[cmd.Name] = true

		if len(cmd.Name) == 0 || len(cmd.Name) > 33 {
			t.Errorf("command %s name length invalid: %d", cmd.Name, len(cmd.Name))
		}
		if cmd.Name != strings.ToLower(cmd.Name) {
			t.Errorf("command %s name is not lowercase", cmd.Name)
		}
		if len(cmd.Description) == 0 || len(cmd.Description) > 100 {
			t.Errorf("command %s description length invalid: %d", cmd.Name, len(cmd.Description))
		}

		for _, opt := range cmd.Options {
			if len(opt.Name) == 0 || len(opt.Name) > 32 {
				t.Errorf("command %s option %s name length invalid: %d", cmd.Name, opt.Name, len(opt.Name))
			}
			if opt.Name != strings.ToLower(opt.Name) {
				t.Errorf("command %s option %s name is not lowercase", cmd.Name, opt.Name)
			}
			if len(opt.Description) == 0 || len(opt.Description) > 100 {
				t.Errorf("command %s option %s description length invalid: %d", cmd.Name, opt.Name, len(opt.Description))
			}
		}
	}

	for exp := range expectedCommands {
		if !foundCommands[exp] {
			t.Errorf("missing expected command: %s", exp)
		}
	}
}

func TestSplitMessage(t *testing.T) {
	shortMsg := "Hello World"
	chunks := splitMessage(shortMsg, 2000)
	if len(chunks) != 1 || chunks[0] != shortMsg {
		t.Fatalf("expected 1 chunk with content %q, got %v", shortMsg, chunks)
	}

	longMsg := strings.Repeat("A", 3500)
	chunks = splitMessage(longMsg, 2000)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if len(chunks[0]) != 2000 || len(chunks[1]) != 1500 {
		t.Fatalf("unexpected chunk lengths: %d, %d", len(chunks[0]), len(chunks[1]))
	}
}

func TestGetOption(t *testing.T) {
	options := []*discordgo.ApplicationCommandInteractionDataOption{
		{
			Name:  "role",
			Type:  discordgo.ApplicationCommandOptionRole,
			Value: "123456789",
		},
		{
			Name:  "message",
			Type:  discordgo.ApplicationCommandOptionString,
			Value: "hello",
		},
	}

	opt := getOption(options, "role")
	if opt == nil || opt.Value != "123456789" {
		t.Fatalf("expected role option with value 123456789, got %v", opt)
	}

	optNotFound := getOption(options, "nonexistent")
	if optNotFound != nil {
		t.Fatalf("expected nil for nonexistent option, got %v", optNotFound)
	}
}

func TestResolveDiscordID(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedID    string
		expectedFmt   string
		expectErr     bool
	}{
		{
			name:        "User Mention standard",
			input:       "<@1478745770298708040>",
			expectedID:  "1478745770298708040",
			expectedFmt: "<@1478745770298708040>",
		},
		{
			name:        "User Mention nickname format",
			input:       "<@!1478745770298708040>",
			expectedID:  "1478745770298708040",
			expectedFmt: "<@1478745770298708040>",
		},
		{
			name:        "Role Mention",
			input:       "<@&987654321098765432>",
			expectedID:  "987654321098765432",
			expectedFmt: "<@&987654321098765432>",
		},
		{
			name:        "Channel Mention",
			input:       "<#112233445566778899>",
			expectedID:  "112233445566778899",
			expectedFmt: "<#112233445566778899>",
		},
		{
			name:        "Custom Emoji standard",
			input:       "<:kekw:998877665544332211>",
			expectedID:  "998877665544332211",
			expectedFmt: "<:kekw:998877665544332211>",
		},
		{
			name:        "Custom Emoji animated",
			input:       "<a:party_blob:998877665544332211>",
			expectedID:  "998877665544332211",
			expectedFmt: "<a:party_blob:998877665544332211>",
		},
		{
			name:        "Raw Snowflake ID",
			input:       "1478745770298708040",
			expectedID:  "1478745770298708040",
			expectedFmt: "<@1478745770298708040>",
		},
		{
			name:        "Mention with whitespace",
			input:       "  <@1478745770298708040>  ",
			expectedID:  "1478745770298708040",
			expectedFmt: "<@1478745770298708040>",
		},
		{
			name:      "Empty input",
			input:     "   ",
			expectErr: true,
		},
		{
			name:      "Invalid input without session",
			input:     "randomstringthatdoesnotexist",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, formatted, err := ResolveDiscordID(nil, "", tt.input)
			if tt.expectErr {
				if err == nil {
					t.Fatalf("expected error, got nil (id: %s, formatted: %s)", id, formatted)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if id != tt.expectedID {
				t.Errorf("expected ID %q, got %q", tt.expectedID, id)
			}
			if formatted != tt.expectedFmt {
				t.Errorf("expected formatted %q, got %q", tt.expectedFmt, formatted)
			}
		})
	}
}

func TestDonationCommandsOptions(t *testing.T) {
	cmdMap := make(map[string]*discordgo.ApplicationCommand)
	for _, cmd := range Commands {
		cmdMap[cmd.Name] = cmd
	}

	setChanCmd := cmdMap["set_donations_channel"]
	if setChanCmd == nil {
		t.Fatal("missing set_donations_channel command")
	}
	if len(setChanCmd.Options) != 1 || setChanCmd.Options[0].Name != "channel" || setChanCmd.Options[0].Required {
		t.Errorf("invalid set_donations_channel options: %+v", setChanCmd.Options)
	}

	setTrackCmd := cmdMap["set_donations_track_corporation"]
	if setTrackCmd == nil {
		t.Fatal("missing set_donations_track_corporation command")
	}
	if len(setTrackCmd.Options) != 1 || setTrackCmd.Options[0].Name != "role" || !setTrackCmd.Options[0].Required {
		t.Errorf("invalid set_donations_track_corporation options: %+v", setTrackCmd.Options)
	}

	lbCmd := cmdMap["donations_leaderboard"]
	if lbCmd == nil {
		t.Fatal("missing donations_leaderboard command")
	}
	if len(lbCmd.Options) != 0 {
		t.Errorf("expected no options for donations_leaderboard, got %d", len(lbCmd.Options))
	}

	postLbCmd := cmdMap["post_donations_leaderboard"]
	if postLbCmd == nil {
		t.Fatal("missing post_donations_leaderboard command")
	}
	if len(postLbCmd.Options) != 0 {
		t.Errorf("expected no options for post_donations_leaderboard, got %d", len(postLbCmd.Options))
	}

	importCmd := cmdMap["import_donations"]
	if importCmd == nil {
		t.Fatal("missing import_donations command")
	}
	if len(importCmd.Options) != 3 {
		t.Errorf("expected 3 options for import_donations, got %d", len(importCmd.Options))
	}
}
