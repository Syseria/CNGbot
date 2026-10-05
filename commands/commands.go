package commands

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"evemaildiscord/config"
	"evemaildiscord/db"
	"evemaildiscord/donations"
	"evemaildiscord/esi"
	"evemaildiscord/roles"

	"github.com/bwmarrin/discordgo"
)

var Commands = []*discordgo.ApplicationCommand{
	{
		Name:        "dashboard",
		Description: "Get the link to the web configuration dashboard",
	},
	{
		Name:        "map_admin",
		Description: "Map a Discord role as a Bot Admin (Only allowed users can run commands)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "The Discord role to authorize as Bot Admin",
				Required:    true,
			},
		},
	},
	{
		Name:        "map_char_id",
		Description: "Manually map an EVE Character ID to a Discord User ID",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionInteger,
				Name:        "character_id",
				Description: "EVE Character ID",
				Required:    true,
			},
			{
				Type:        discordgo.ApplicationCommandOptionUser,
				Name:        "user",
				Description: "The Discord user to map",
				Required:    true,
			},
		},
	},
	{
		Name:        "map_corp_id",
		Description: "Manually map an EVE Corporation ID to a Discord Role ID",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionInteger,
				Name:        "corporation_id",
				Description: "EVE Corporation ID",
				Required:    true,
			},
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "The Discord role to map",
				Required:    true,
			},
		},
	},
	{
		Name:        "map_corp",
		Description: "Auto-map a Discord role to an EVE Corporation ID matching its exact name",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "The Discord role to search and map",
				Required:    true,
			},
		},
	},
	{
		Name:        "map_alliance",
		Description: "Map an EVE Alliance ID to a Discord Role ID",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionInteger,
				Name:        "alliance_id",
				Description: "EVE Alliance ID",
				Required:    true,
			},
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "The Discord role to map",
				Required:    true,
			},
		},
	},
	{
		Name:        "map_greet_role",
		Description: "Map a Corporation or Alliance role to trigger a greeting message when auto-assigned",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "The Discord role to toggle for greetings",
				Required:    true,
			},
		},
	},
	{
		Name:        "seat_needed_roles",
		Description: "Toggle requiring SeAT registration for a Corporation or Alliance role",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "The Corporation or Alliance role to toggle for SeAT requirement",
				Required:    true,
			},
		},
	},
	{
		Name:        "req_corp_for_alliance",
		Description: "Toggle requiring a mapped Corporation role for an Alliance role",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "The mapped Alliance role to toggle requirement for",
				Required:    true,
			},
		},
	},
	{
		Name:        "rm_corp",
		Description: "Remove an EVE Corporation mapping using its Discord Role",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "The Discord role to remove mapping for",
				Required:    true,
			},
		},
	},
	{
		Name:        "rm_corp_id",
		Description: "Remove an EVE Corporation mapping using its EVE ID or Discord Role ID",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "id",
				Description: "The EVE Corporation ID or Discord Role ID",
				Required:    true,
			},
		},
	},
	{
		Name:        "rm_alliance",
		Description: "Remove an EVE Alliance mapping using its Discord Role",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "The Discord role to remove mapping for",
				Required:    true,
			},
		},
	},
	{
		Name:        "rm_char_id",
		Description: "Remove an EVE Character mapping using its EVE ID or Discord User ID",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "id",
				Description: "The EVE Character ID or Discord User ID",
				Required:    true,
			},
		},
	},
	{
		Name:        "map_char",
		Description: "Auto-map a user based on their Discord nickname or username",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionUser,
				Name:        "user",
				Description: "The Discord user to map",
				Required:    true,
			},
		},
	},
	{
		Name:        "sync",
		Description: "Force an immediate manual verification of all mapped users' roles",
	},
	{
		Name:        "report",
		Description: "Generate a report of unmapped users and automatically prune users who left the server",
	},
	{
		Name:        "help",
		Description: "Display list of available bot commands and usage",
	},
	{
		Name:        "exclude_sync",
		Description: "Exclude (or toggle) a Discord user from role synchronization checks",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionUser,
				Name:        "user",
				Description: "The Discord user to exclude or unexclude from role sync checks",
				Required:    true,
			},
		},
	},
	{
		Name:        "exclude_map",
		Description: "Exclude (or toggle) a Discord user from automatic mapping and unmapped reporting",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionUser,
				Name:        "user",
				Description: "The Discord user to exclude or unexclude from automapping",
				Required:    true,
			},
		},
	},
	{
		Name:        "exclude_corp_standing",
		Description: "Exclude (or toggle) a mapped Corporation role from receiving standing roles",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "The mapped Corporation Discord role to exclude/unexclude from standings",
				Required:    true,
			},
		},
	},
	{
		Name:        "exclude_2fa",
		Description: "Exclude (or toggle) a mapped Corporation/Alliance role from requiring 2FA",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "The Discord role to exclude/unexclude from 2FA requirement",
				Required:    true,
			},
		},
	},
	{
		Name:        "toggle_2fa",
		Description: "Toggle requiring 2FA code via EVE-mail to receive mapped Discord roles",
	},
	{
		Name:        "set_log_channel",
		Description: "Set the channel where logs should be posted",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionChannel,
				Name:        "channel",
				Description: "The Discord channel for logs",
				Required:    true,
				ChannelTypes: []discordgo.ChannelType{
					discordgo.ChannelTypeGuildText,
					discordgo.ChannelTypeGuildNews,
				},
			},
		},
	},
	{
		Name:        "set_events_channel",
		Description: "Set or disable the channel where newly created calendar events will be posted",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionChannel,
				Name:        "channel",
				Description: "The Discord channel for events (omit to disable)",
				Required:    false,
				ChannelTypes: []discordgo.ChannelType{
					discordgo.ChannelTypeGuildText,
					discordgo.ChannelTypeGuildNews,
				},
			},
		},
	},
	{
		Name:        "set_mail_channel",
		Description: "Set or disable the channel where EVE mails will be posted",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionChannel,
				Name:        "channel",
				Description: "The Discord channel for EVE mails (omit to disable)",
				Required:    false,
				ChannelTypes: []discordgo.ChannelType{
					discordgo.ChannelTypeGuildText,
					discordgo.ChannelTypeGuildNews,
				},
			},
		},
	},
	{
		Name:        "toggle_logs",
		Description: "Toggle posting role addition/removal logs to the designated channel",
	},
	{
		Name:        "toggle_automap",
		Description: "Toggle automatic character mapping of new server members on join",
	},
	{
		Name:        "set_greeting",
		Description: "Configure automatic greeting message for new members on join (omit options to disable)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionChannel,
				Name:        "channel",
				Description: "The channel to post greetings in (omit to disable)",
				Required:    false,
				ChannelTypes: []discordgo.ChannelType{
					discordgo.ChannelTypeGuildText,
					discordgo.ChannelTypeGuildNews,
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "message",
				Description: "The greeting message (omit to disable)",
				Required:    false,
			},
		},
	},
	{
		Name:        "set_guest",
		Description: "Set the guest role applied to mapped users whose Corp/Alliance is not yet mapped",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "The Discord role to use as guest role (omit to disable)",
				Required:    false,
			},
		},
	},
	{
		Name:        "set_missing_seat_role",
		Description: "Set a role to assign to users who need to log into SeAT but haven't (omit to disable)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "The Discord role to assign (omit to disable)",
				Required:    false,
			},
		},
	},
	{
		Name:        "map_standing_terrible",
		Description: "Map a Discord role to EVE Online Terrible standing (-10.0 to -5.0)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "The Discord role for Terrible standing (omit to remove mapping)",
				Required:    false,
			},
		},
	},
	{
		Name:        "map_standing_bad",
		Description: "Map a Discord role to EVE Online Bad standing (< 0.0 to > -5.0)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "The Discord role for Bad standing (omit to remove mapping)",
				Required:    false,
			},
		},
	},
	{
		Name:        "map_standing_neutral",
		Description: "Map a Discord role to EVE Online Neutral standing (0.0)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "The Discord role for Neutral standing (omit to remove mapping)",
				Required:    false,
			},
		},
	},
	{
		Name:        "map_standing_good",
		Description: "Map a Discord role to EVE Online Good standing (> 0.0 to < 5.0)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "The Discord role for Good standing (omit to remove mapping)",
				Required:    false,
			},
		},
	},
	{
		Name:        "map_standing_excellent",
		Description: "Map a Discord role to EVE Online Excellent standing (+5.0 to +10.0)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "The Discord role for Excellent standing (omit to remove mapping)",
				Required:    false,
			},
		},
	},
	{
		Name:        "char_info",
		Description: "Display detailed mapping, exclusion status, and automatically assigned roles for a Discord user",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionUser,
				Name:        "user",
				Description: "The Discord user to check",
				Required:    true,
			},
		},
	},
	{
		Name:        "id",
		Description: "Get the Discord ID and mention format for a user, channel, role, or emoji",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "target",
				Description: "The user, channel, role, emoji, or ID to look up",
				Required:    true,
			},
		},
	},
	{
		Name:        "set_donations_channel",
		Description: "Sets or disables the channel for donation announcements",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionChannel,
				Name:        "channel",
				Description: "The channel to post in",
				Required:    false,
				ChannelTypes: []discordgo.ChannelType{
					discordgo.ChannelTypeGuildText,
					discordgo.ChannelTypeGuildNews,
				},
			},
		},
	},
	{
		Name:        "set_donations_track_corporation",
		Description: "Toggles tracking for a specific corporation via its mapped Discord role",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "The role mapped to the EVE Corporation",
				Required:    true,
			},
		},
	},
	{
		Name:        "donations_leaderboard",
		Description: "Displays the top 50 donors of all time to the tracked corporations",
	},
	{
		Name:        "post_donations_leaderboard",
		Description: "Posts the leaderboard to the configured donations channel",
	},
	{
		Name:        "import_donations",
		Description: "Import pasted corporation wallet journal data to backfill donation history",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "data",
				Description: "Pasted text from EVE corporation wallet journal",
				Required:    false,
			},
			{
				Type:        discordgo.ApplicationCommandOptionAttachment,
				Name:        "file",
				Description: "Text file (.txt) containing exported wallet journal",
				Required:    false,
			},
			{
				Type:        discordgo.ApplicationCommandOptionRole,
				Name:        "role",
				Description: "Target corporation role (optional if auto-detected)",
				Required:    false,
			},
		},
	},
	{
		Name:		 "courier_fee",
		Description: "Calculates the fee for your courier contract",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionNumber,
				Name:		 "volume",
				Description: "The volume of the cargo to be hauled. Defaults to 0",
				Required:	 true,
			},
			{
				Type:        discordgo.ApplicationCommandOptionInteger,
				Name:		 "jumps",
				Description: "The numbers of jumps from start to finish. Defaults to 1",
				Required:	 true,
			},
			{
				Type:        discordgo.ApplicationCommandOptionNumber,
				Name:		 "estimate",
				Description: "The estimated value of the cargo to be hauled. Defaults to 0",
				Required:	 true,
			},
		},
	},
}

func RegisterCommands(s *discordgo.Session) error {
	log.Printf("Registering %d global slash commands...", len(Commands))
	_, err := s.ApplicationCommandBulkOverwrite(s.State.User.ID, "", Commands)
	if err != nil {
		log.Printf("Error registering slash commands: %v", err)
		return err
	}
	log.Println("Global slash commands registered successfully.")
	return nil
}

func getOption(options []*discordgo.ApplicationCommandInteractionDataOption, name string) *discordgo.ApplicationCommandInteractionDataOption {
	for _, opt := range options {
		if opt.Name == name {
			return opt
		}
	}
	return nil
}

func sendResponse(s *discordgo.Session, interaction *discordgo.Interaction, content string, ephemeral ...bool) {
	flags := discordgo.MessageFlagsEphemeral

	if len(content) <= 2000 {
		_, err := s.InteractionResponseEdit(interaction, &discordgo.WebhookEdit{
			Content: &content,
		})
		if err != nil {
			log.Printf("InteractionResponseEdit failed, falling back to channel message: %v", err)
			_, _ = s.ChannelMessageSend(interaction.ChannelID, content)
		}
		return
	}

	chunks := splitMessage(content, 2000)
	for idx, chunk := range chunks {
		if idx == 0 {
			_, err := s.InteractionResponseEdit(interaction, &discordgo.WebhookEdit{
				Content: &chunk,
			})
			if err != nil {
				_, _ = s.ChannelMessageSend(interaction.ChannelID, chunk)
			}
		} else {
			_, err := s.FollowupMessageCreate(interaction, true, &discordgo.WebhookParams{
				Content: chunk,
				Flags:   flags,
			})
			if err != nil {
				_, _ = s.ChannelMessageSend(interaction.ChannelID, chunk)
			}
		}
	}
}

func splitMessage(msg string, maxLen int) []string {
	var chunks []string
	for len(msg) > 0 {
		if len(msg) <= maxLen {
			chunks = append(chunks, msg)
			break
		}
		end := maxLen
		lastNewline := strings.LastIndex(msg[:end], "\n")
		if lastNewline > 0 {
			end = lastNewline + 1
		}
		chunks = append(chunks, msg[:end])
		msg = msg[end:]
	}
	return chunks
}

func InteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	data := i.ApplicationCommandData()
	cmdName := data.Name

	userID := ""
	if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	}

	guildID := i.GuildID

	log.Printf("Received slash command /%s in guild %s from <@%s>", cmdName, guildID, userID)

	// Defer the interaction response immediately to satisfy Discord's 3-second deadline
	errDefer := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
	if errDefer != nil {
		log.Printf("Error deferring interaction: %v", errDefer)
	}

	// Native Discord Administrator check
	var hasAdminRole bool
	if i.Member != nil && (i.Member.Permissions&discordgo.PermissionAdministrator) != 0 {
		hasAdminRole = true
	}

	// Read mapped admin roles from database
	var adminRoleCount int
	errCount := db.DB.QueryRow("SELECT COUNT(*) FROM admin_roles WHERE guild_id = ?", guildID).Scan(&adminRoleCount)

	if !hasAdminRole && errCount == nil && adminRoleCount > 0 {
		if i.Member != nil {
			rowsAdmins, errQuery := db.DB.Query("SELECT role_id FROM admin_roles WHERE guild_id = ?", guildID)
			if errQuery == nil {
				var adminRolesList []string
				for rowsAdmins.Next() {
					var rID string
					if rowsAdmins.Scan(&rID) == nil {
						adminRolesList = append(adminRolesList, rID)
					}
				}
				_ = rowsAdmins.Close()

				for _, rID := range adminRolesList {
					if roles.HasRole(i.Member.Roles, rID) {
						hasAdminRole = true
						break
					}
				}
			}
		}
	} else if adminRoleCount == 0 {
		// If NO admin roles are mapped, /map_admin and /dashboard are allowed
		if cmdName != "map_admin" && cmdName != "dashboard" && !hasAdminRole {
			warningMsg := fmt.Sprintf("Command ignored: User <@%s> tried to execute command `/%s`, but no admin roles are mapped yet. Only `/map_admin` and `/dashboard` are allowed right now.", userID, cmdName)
			db.DiscordLog(s, guildID, warningMsg)
			sendResponse(s, i.Interaction, "No admin roles are mapped yet. Only `/map_admin` and `/dashboard` are allowed right now.")
			return
		}
		hasAdminRole = true
	}

	// Enforce admin permission lock for mapped admins
	if !hasAdminRole {
		if adminRoleCount > 0 {
			warningMsg := fmt.Sprintf("Security Warning: User <@%s> tried to execute command `/%s` but is not in an Admin role. Command ignored.", userID, cmdName)
			db.DiscordLog(s, guildID, warningMsg)
		}
		sendResponse(s, i.Interaction, "You do not have permission to execute bot commands.")
		return
	}

	switch cmdName {
	case "dashboard":
		dashboardURL := config.GetDashboardURL()
		if guildID != "" {
			dashboardURL = fmt.Sprintf("%s/dashboard/guild?id=%s", dashboardURL, guildID)
		}
		msg := fmt.Sprintf("Configure your server in the CNGBot Web Dashboard:\n%s", dashboardURL)
		sendResponse(s, i.Interaction, msg)
		return

	case "map_admin":
		roleOpt := getOption(data.Options, "role")
		if roleOpt == nil {
			sendResponse(s, i.Interaction, "You must specify a role.")
			return
		}

		var roleID string
		if r := roleOpt.RoleValue(s, i.GuildID); r != nil {
			roleID = r.ID
		} else if str, ok := roleOpt.Value.(string); ok {
			roleID = str
		}

		if roleID == "" {
			sendResponse(s, i.Interaction, "Invalid role specified.")
			return
		}

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			_, err := db.DB.Exec("INSERT OR REPLACE INTO admin_roles (guild_id, role_id) VALUES (?, ?)", guildID, roleID)
			if err != nil {
				return err
			}
			sendResponse(s, i.Interaction, fmt.Sprintf("Role <@&%s> has been successfully mapped as an Admin role. Users in this role can now manage the bot.", roleID))
			return nil
		})

	case "map_char_id":
		charOpt := getOption(data.Options, "character_id")
		userOpt := getOption(data.Options, "user")
		if charOpt == nil || userOpt == nil {
			sendResponse(s, i.Interaction, "Usage: /map_char_id character_id:<ID> user:<@User>")
			return
		}

		eveID := int(charOpt.IntValue())
		targetUser := userOpt.UserValue(s)
		if targetUser == nil {
			sendResponse(s, i.Interaction, "Invalid user specified.")
			return
		}
		discordID := targetUser.ID

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			_, err := db.DB.Exec("DELETE FROM character_to_discord WHERE discord_id = ?", discordID)
			if err != nil {
				return err
			}

			_, err = db.DB.Exec("INSERT OR REPLACE INTO character_to_discord (eve_id, discord_id) VALUES (?, ?)", eveID, discordID)
			if err != nil {
				return err
			}
			sendResponse(s, i.Interaction, fmt.Sprintf("Mapped EVE Character %d (https://evewho.com/character/%d) to Discord User <@%s>", eveID, eveID, discordID))
			go roles.CheckRoles(s)
			return nil
		})

	case "map_corp_id":
		corpOpt := getOption(data.Options, "corporation_id")
		roleOpt := getOption(data.Options, "role")
		if corpOpt == nil || roleOpt == nil {
			sendResponse(s, i.Interaction, "Usage: /map_corp_id corporation_id:<ID> role:<@Role>")
			return
		}

		corpID := int(corpOpt.IntValue())
		var roleID string
		if r := roleOpt.RoleValue(s, i.GuildID); r != nil {
			roleID = r.ID
		} else if str, ok := roleOpt.Value.(string); ok {
			roleID = str
		}

		if roleID == "" {
			sendResponse(s, i.Interaction, "Invalid role specified.")
			return
		}

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			_, err := db.DB.Exec("INSERT OR REPLACE INTO corp_to_role (guild_id, corp_id, role_id) VALUES (?, ?, ?)", guildID, corpID, roleID)
			if err != nil {
				return err
			}
			sendResponse(s, i.Interaction, fmt.Sprintf("Mapped Corp %d to Role <@&%s>", corpID, roleID))
			go roles.CheckRoles(s)
			return nil
		})

	case "map_corp":
		roleOpt := getOption(data.Options, "role")
		if roleOpt == nil {
			sendResponse(s, i.Interaction, "Usage: /map_corp role:<@Role>")
			return
		}

		var roleID string
		var roleName string
		if r := roleOpt.RoleValue(s, i.GuildID); r != nil {
			roleID = r.ID
			roleName = r.Name
		} else if str, ok := roleOpt.Value.(string); ok {
			roleID = str
		}

		if roleName == "" && roleID != "" {
			rolesList, err := s.GuildRoles(i.GuildID)
			if err == nil {
				for _, r := range rolesList {
					if r.ID == roleID {
						roleName = r.Name
						break
					}
				}
			}
		}

		if roleID == "" || roleName == "" {
			sendResponse(s, i.Interaction, "Could not find role details in the server.")
			return
		}

		// Query ESI to search for the Corporation ID matching that role's name
		namesArray := []string{roleName}
		bodyBytes, _ := json.Marshal(namesArray)
		resp, err := esi.Post("https://esi.evetech.net/latest/universe/ids/", "application/json", bytes.NewBuffer(bodyBytes))
		if err != nil {
			sendResponse(s, i.Interaction, fmt.Sprintf("Error searching for corporation '%s': %v", roleName, err))
			return
		}

		var universeResp struct {
			Corporations []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"corporations"`
		}

		if resp.StatusCode == 200 {
			if err := json.NewDecoder(resp.Body).Decode(&universeResp); err != nil {
				sendResponse(s, i.Interaction, "Error decoding corporation search response.")
				_ = resp.Body.Close()
				return
			}
		} else {
			sendResponse(s, i.Interaction, fmt.Sprintf("Error searching for corporation '%s' (Status: %d).", roleName, resp.StatusCode))
			_ = resp.Body.Close()
			return
		}
		_ = resp.Body.Close()

		if len(universeResp.Corporations) == 0 {
			// Try fallback to lowercase role name
			lowerRoleName := strings.ToLower(roleName)
			namesArray = []string{lowerRoleName}
			bodyBytes, _ = json.Marshal(namesArray)
			resp, err = esi.Post("https://esi.evetech.net/latest/universe/ids/", "application/json", bytes.NewBuffer(bodyBytes))
			if err != nil {
				sendResponse(s, i.Interaction, fmt.Sprintf("Error searching for corporation '%s' (lowercase fallback): %v", lowerRoleName, err))
				return
			}

			if resp.StatusCode == 200 {
				if err := json.NewDecoder(resp.Body).Decode(&universeResp); err != nil {
					sendResponse(s, i.Interaction, "Error decoding corporation search response during lowercase fallback.")
					_ = resp.Body.Close()
					return
				}
			} else {
				sendResponse(s, i.Interaction, fmt.Sprintf("Error searching for corporation '%s' (lowercase fallback, Status: %d).", lowerRoleName, resp.StatusCode))
				_ = resp.Body.Close()
				return
			}
			_ = resp.Body.Close()

			if len(universeResp.Corporations) == 0 {
				sendResponse(s, i.Interaction, fmt.Sprintf("No EVE Corporation found matching exactly '%s' or '%s'.", roleName, lowerRoleName))
				return
			}
			roleName = lowerRoleName
		}

		corpID := universeResp.Corporations[0].ID

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			_, err := db.DB.Exec("INSERT OR REPLACE INTO corp_to_role (guild_id, corp_id, role_id) VALUES (?, ?, ?)", guildID, corpID, roleID)
			if err != nil {
				return err
			}
			sendResponse(s, i.Interaction, fmt.Sprintf("Mapped EVE Corporation '%s' (ID: %d) to Role <@&%s>", roleName, corpID, roleID))
			go roles.CheckRoles(s)
			return nil
		})

	case "map_alliance":
		allianceOpt := getOption(data.Options, "alliance_id")
		roleOpt := getOption(data.Options, "role")
		if allianceOpt == nil || roleOpt == nil {
			sendResponse(s, i.Interaction, "Usage: /map_alliance alliance_id:<ID> role:<@Role>")
			return
		}

		allianceID := int(allianceOpt.IntValue())
		var roleID string
		if r := roleOpt.RoleValue(s, i.GuildID); r != nil {
			roleID = r.ID
		} else if str, ok := roleOpt.Value.(string); ok {
			roleID = str
		}

		if roleID == "" {
			sendResponse(s, i.Interaction, "Invalid role specified.")
			return
		}

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			_, err := db.DB.Exec("INSERT OR REPLACE INTO alliance_to_role (guild_id, alliance_id, role_id) VALUES (?, ?, ?)", guildID, allianceID, roleID)
			if err != nil {
				return err
			}
			sendResponse(s, i.Interaction, fmt.Sprintf("Mapped Alliance %d to Role <@&%s>", allianceID, roleID))
			return nil
		})

	case "map_greet_role":
		roleOpt := getOption(data.Options, "role")
		if roleOpt == nil {
			sendResponse(s, i.Interaction, "Usage: /map_greet_role role:<@Role>")
			return
		}

		var roleID string
		if r := roleOpt.RoleValue(s, i.GuildID); r != nil {
			roleID = r.ID
		} else if str, ok := roleOpt.Value.(string); ok {
			roleID = str
		}

		if roleID == "" {
			sendResponse(s, i.Interaction, "Invalid role specified.")
			return
		}

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			var count int
			err := db.DB.QueryRow("SELECT (SELECT COUNT(*) FROM corp_to_role WHERE guild_id = ? AND role_id = ?) + (SELECT COUNT(*) FROM alliance_to_role WHERE guild_id = ? AND role_id = ?)", guildID, roleID, guildID, roleID).Scan(&count)
			if err != nil {
				return err
			}
			if count == 0 {
				sendResponse(s, i.Interaction, "That role is not currently mapped as a Corporation or Alliance.")
				return nil
			}

			var exists int
			err = db.DB.QueryRow("SELECT COUNT(*) FROM greet_on_role WHERE guild_id = ? AND role_id = ?", guildID, roleID).Scan(&exists)
			if err != nil {
				return err
			}

			if exists > 0 {
				_, err = db.DB.Exec("DELETE FROM greet_on_role WHERE guild_id = ? AND role_id = ?", guildID, roleID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, fmt.Sprintf("Removed <@&%s> from greet-on-role list.", roleID))
			} else {
				_, err = db.DB.Exec("INSERT OR REPLACE INTO greet_on_role (guild_id, role_id) VALUES (?, ?)", guildID, roleID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, fmt.Sprintf("Added <@&%s> to greet-on-role list.", roleID))
			}
			return nil
		})

	case "seat_needed_roles":
		roleOpt := getOption(data.Options, "role")
		if roleOpt == nil {
			sendResponse(s, i.Interaction, "Usage: /seat_needed_roles role:<@Role>")
			return
		}

		var roleID string
		if r := roleOpt.RoleValue(s, i.GuildID); r != nil {
			roleID = r.ID
		} else if str, ok := roleOpt.Value.(string); ok {
			roleID = str
		}

		if roleID == "" {
			sendResponse(s, i.Interaction, "Invalid role specified.")
			return
		}

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			var count int
			err := db.DB.QueryRow("SELECT (SELECT COUNT(*) FROM corp_to_role WHERE guild_id = ? AND role_id = ?) + (SELECT COUNT(*) FROM alliance_to_role WHERE guild_id = ? AND role_id = ?)", guildID, roleID, guildID, roleID).Scan(&count)
			if err != nil {
				return err
			}
			if count == 0 {
				sendResponse(s, i.Interaction, "That role is not currently mapped as a Corporation or Alliance.")
				return nil
			}

			var exists int
			err = db.DB.QueryRow("SELECT COUNT(*) FROM seat_needed_roles WHERE guild_id = ? AND role_id = ?", guildID, roleID).Scan(&exists)
			if err != nil {
				return err
			}

			if exists > 0 {
				_, err = db.DB.Exec("DELETE FROM seat_needed_roles WHERE guild_id = ? AND role_id = ?", guildID, roleID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, fmt.Sprintf("Role <@&%s> removed from SeAT required roles list. Users will no longer need to be active in SeAT to receive this role.", roleID))
			} else {
				_, err = db.DB.Exec("INSERT OR REPLACE INTO seat_needed_roles (guild_id, role_id) VALUES (?, ?)", guildID, roleID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, fmt.Sprintf("Role <@&%s> added to SeAT required roles list. Users will now need to be active in SeAT to receive this role.", roleID))
			}
			go roles.CheckRoles(s)
			return nil
		})

	case "req_corp_for_alliance":
		roleOpt := getOption(data.Options, "role")
		if roleOpt == nil {
			sendResponse(s, i.Interaction, "Usage: /req_corp_for_alliance role:<@Role>")
			return
		}

		var roleID string
		if r := roleOpt.RoleValue(s, i.GuildID); r != nil {
			roleID = r.ID
		} else if str, ok := roleOpt.Value.(string); ok {
			roleID = str
		}

		if roleID == "" {
			sendResponse(s, i.Interaction, "Invalid role specified.")
			return
		}

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			var count int
			err := db.DB.QueryRow("SELECT COUNT(*) FROM alliance_to_role WHERE guild_id = ? AND role_id = ?", guildID, roleID).Scan(&count)
			if err != nil {
				return err
			}
			if count == 0 {
				sendResponse(s, i.Interaction, "That role is not currently mapped as an Alliance.")
				return nil
			}

			var exists int
			err = db.DB.QueryRow("SELECT COUNT(*) FROM alliance_required_corp_roles WHERE guild_id = ? AND role_id = ?", guildID, roleID).Scan(&exists)
			if err != nil {
				return err
			}

			if exists > 0 {
				_, err = db.DB.Exec("DELETE FROM alliance_required_corp_roles WHERE guild_id = ? AND role_id = ?", guildID, roleID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, fmt.Sprintf("Role <@&%s> removed from alliance required corp roles list. Users will no longer need a mapped Corporation role to receive this Alliance role.", roleID))
			} else {
				_, err = db.DB.Exec("INSERT OR REPLACE INTO alliance_required_corp_roles (guild_id, role_id) VALUES (?, ?)", guildID, roleID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, fmt.Sprintf("Role <@&%s> added to alliance required corp roles list. Users will now need a mapped Corporation role to receive this Alliance role.", roleID))
			}
			go roles.CheckRoles(s)
			return nil
		})

	case "rm_corp":
		roleOpt := getOption(data.Options, "role")
		if roleOpt == nil {
			sendResponse(s, i.Interaction, "Usage: /rm_corp role:<@Role>")
			return
		}

		var roleID string
		if r := roleOpt.RoleValue(s, i.GuildID); r != nil {
			roleID = r.ID
		} else if str, ok := roleOpt.Value.(string); ok {
			roleID = str
		}

		if roleID == "" {
			sendResponse(s, i.Interaction, "Invalid role specified.")
			return
		}

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			res, err := db.DB.Exec("DELETE FROM corp_to_role WHERE guild_id = ? AND role_id = ?", guildID, roleID)
			if err != nil {
				return err
			}
			rowsAffected, _ := res.RowsAffected()
			if rowsAffected > 0 {
				sendResponse(s, i.Interaction, fmt.Sprintf("Successfully removed EVE Corporation mapping for Role <@&%s>.", roleID))
			} else {
				sendResponse(s, i.Interaction, fmt.Sprintf("No active EVE Corporation mapping found for Role <@&%s>.", roleID))
			}
			return nil
		})

	case "rm_alliance":
		roleOpt := getOption(data.Options, "role")
		if roleOpt == nil {
			sendResponse(s, i.Interaction, "Usage: /rm_alliance role:<@Role>")
			return
		}

		var roleID string
		if r := roleOpt.RoleValue(s, i.GuildID); r != nil {
			roleID = r.ID
		} else if str, ok := roleOpt.Value.(string); ok {
			roleID = str
		}

		if roleID == "" {
			sendResponse(s, i.Interaction, "Invalid role specified.")
			return
		}

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			res, err := db.DB.Exec("DELETE FROM alliance_to_role WHERE guild_id = ? AND role_id = ?", guildID, roleID)
			if err != nil {
				return err
			}
			rowsAffected, _ := res.RowsAffected()
			if rowsAffected > 0 {
				sendResponse(s, i.Interaction, fmt.Sprintf("Successfully removed EVE Alliance mapping for Role <@&%s>.", roleID))
			} else {
				sendResponse(s, i.Interaction, fmt.Sprintf("No active EVE Alliance mapping found for Role <@&%s>.", roleID))
			}
			return nil
		})

	case "rm_char_id":
		idOpt := getOption(data.Options, "id")
		if idOpt == nil {
			sendResponse(s, i.Interaction, "Usage: /rm_char_id id:<EVE_CHAR_ID_OR_DISCORD_ID>")
			return
		}

		inputID := idOpt.StringValue()
		discordPattern := regexp.MustCompile(`<@!?(\d+)>`)
		matches := discordPattern.FindStringSubmatch(inputID)
		if len(matches) == 2 {
			inputID = matches[1]
		}

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			res, err := db.DB.Exec("DELETE FROM character_to_discord WHERE eve_id = ? OR discord_id = ?", inputID, inputID)
			if err != nil {
				return err
			}
			rowsAffected, _ := res.RowsAffected()
			if rowsAffected > 0 {
				sendResponse(s, i.Interaction, fmt.Sprintf("Successfully removed EVE Character mapping for ID `%s`.", inputID))
			} else {
				sendResponse(s, i.Interaction, fmt.Sprintf("No active EVE Character mapping found for ID `%s`.", inputID))
			}
			return nil
		})

	case "rm_corp_id":
		idOpt := getOption(data.Options, "id")
		if idOpt == nil {
			sendResponse(s, i.Interaction, "Usage: /rm_corp_id id:<EVE_CORP_ID_OR_ROLE_ID>")
			return
		}

		inputID := idOpt.StringValue()
		rolePattern := regexp.MustCompile(`<@&(\d+)>`)
		matches := rolePattern.FindStringSubmatch(inputID)
		if len(matches) == 2 {
			inputID = matches[1]
		}

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			res, err := db.DB.Exec("DELETE FROM corp_to_role WHERE guild_id = ? AND (corp_id = ? OR role_id = ?)", guildID, inputID, inputID)
			if err != nil {
				return err
			}
			rowsAffected, _ := res.RowsAffected()
			if rowsAffected > 0 {
				sendResponse(s, i.Interaction, fmt.Sprintf("Successfully removed EVE Corporation mapping for ID `%s`.", inputID))
			} else {
				sendResponse(s, i.Interaction, fmt.Sprintf("No active EVE Corporation mapping found for ID `%s`.", inputID))
			}
			return nil
		})

	case "map_char":
		userOpt := getOption(data.Options, "user")
		if userOpt == nil {
			sendResponse(s, i.Interaction, "Usage: /map_char user:<@User>")
			return
		}

		targetUser := userOpt.UserValue(s)
		if targetUser == nil {
			sendResponse(s, i.Interaction, "Could not fetch user details.")
			return
		}

		discordID := targetUser.ID
		member, errMember := s.GuildMember(i.GuildID, discordID)
		if errMember != nil {
			sendResponse(s, i.Interaction, "Could not fetch member info for the user.")
			return
		}

		searchName := member.Nick
		if searchName == "" {
			if targetUser.GlobalName != "" {
				searchName = targetUser.GlobalName
			} else {
				searchName = targetUser.Username
			}
		}

		namesArray := []string{searchName}
		bodyBytes, _ := json.Marshal(namesArray)
		resp, err := esi.Post("https://esi.evetech.net/latest/universe/ids/", "application/json", bytes.NewBuffer(bodyBytes))
		if err != nil {
			sendResponse(s, i.Interaction, fmt.Sprintf("Error searching for character '%s': %v", searchName, err))
			return
		}

		var universeResp struct {
			Characters []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"characters"`
		}

		if resp.StatusCode == 200 {
			if err := json.NewDecoder(resp.Body).Decode(&universeResp); err != nil {
				sendResponse(s, i.Interaction, "Error decoding search response.")
				_ = resp.Body.Close()
				return
			}
		} else {
			sendResponse(s, i.Interaction, fmt.Sprintf("Error searching for character '%s' (Status: %d).", searchName, resp.StatusCode))
			_ = resp.Body.Close()
			return
		}
		_ = resp.Body.Close()

		if len(universeResp.Characters) == 0 {
			lowerSearchName := strings.ToLower(searchName)
			namesArray = []string{lowerSearchName}
			bodyBytes, _ = json.Marshal(namesArray)
			resp, err = esi.Post("https://esi.evetech.net/latest/universe/ids/", "application/json", bytes.NewBuffer(bodyBytes))
			if err != nil {
				sendResponse(s, i.Interaction, fmt.Sprintf("Error searching for character '%s' (lowercase fallback): %v", lowerSearchName, err))
				return
			}

			if resp.StatusCode == 200 {
				if err := json.NewDecoder(resp.Body).Decode(&universeResp); err != nil {
					sendResponse(s, i.Interaction, "Error decoding search response during lowercase fallback.")
					_ = resp.Body.Close()
					return
				}
			} else {
				sendResponse(s, i.Interaction, fmt.Sprintf("Error searching for character '%s' (lowercase fallback, Status: %d).", lowerSearchName, resp.StatusCode))
				_ = resp.Body.Close()
				return
			}
			_ = resp.Body.Close()

			if len(universeResp.Characters) == 0 {
				sendResponse(s, i.Interaction, fmt.Sprintf("No EVE character found matching exactly '%s' or '%s'.", searchName, lowerSearchName))
				return
			}
			searchName = lowerSearchName
		}

		eveID := universeResp.Characters[0].ID

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			_, err := db.DB.Exec("DELETE FROM character_to_discord WHERE discord_id = ?", discordID)
			if err != nil {
				return err
			}

			_, err = db.DB.Exec("INSERT OR REPLACE INTO character_to_discord (eve_id, discord_id) VALUES (?, ?)", eveID, discordID)
			if err != nil {
				return err
			}
			sendResponse(s, i.Interaction, fmt.Sprintf("Mapped EVE Character '%s' (ID: %d - https://evewho.com/character/%d) to Discord User <@%s>", searchName, eveID, eveID, discordID))
			go roles.CheckRoles(s)
			return nil
		})

	case "sync":
		sendResponse(s, i.Interaction, "Starting manual role synchronization...")
		go func() {
			roles.CheckRoles(s)
			sendResponse(s, i.Interaction, "Manual role synchronization complete!")
		}()

	case "report":
		sendResponse(s, i.Interaction, "Generating server membership and mapping report...", true)
		go func() {
			members, err := s.GuildMembers(i.GuildID, "", 1000)
			if err != nil {
				sendResponse(s, i.Interaction, fmt.Sprintf("Error fetching Discord guild members: %v", err), true)
				return
			}

			guildMemberMap := make(map[string]bool)
			for _, mem := range members {
				if mem.User != nil && !mem.User.Bot {
					guildMemberMap[mem.User.ID] = true
				}
			}

			rows, err := db.DB.Query("SELECT DISTINCT discord_id FROM character_to_discord")
			if err != nil {
				sendResponse(s, i.Interaction, fmt.Sprintf("Error querying database: %v", err), true)
				return
			}

			mappedUserMap := make(map[string]bool)
			var leftServer []string

			for rows.Next() {
				var dID string
				if err := rows.Scan(&dID); err == nil {
					mappedUserMap[dID] = true
					if !guildMemberMap[dID] {
						leftServer = append(leftServer, dID)
					}
				}
			}
			_ = rows.Close()

			var cleanedUpCount int
			var actuallyCleanedUp []string

			// Ensure our tracking table exists
			_, _ = db.DB.Exec("CREATE TABLE IF NOT EXISTS cleaned_up_users (guild_id TEXT, discord_id TEXT, PRIMARY KEY (guild_id, discord_id))")

			for _, dID := range leftServer {
				// In multi-server mode, leaving one server does not delete global mapping if they could be on other servers
				// But we clean up guild-specific exclusions/attempts
				_, _ = db.DB.Exec("DELETE FROM auto_map_attempts WHERE guild_id = ? AND discord_id = ?", guildID, dID)
				_, _ = db.DB.Exec("DELETE FROM excluded_users WHERE guild_id = ? AND discord_id = ?", guildID, dID)
				_, _ = db.DB.Exec("DELETE FROM excluded_mappings WHERE guild_id = ? AND discord_id = ?", guildID, dID)
				
				var exists int
				_ = db.DB.QueryRow("SELECT COUNT(*) FROM cleaned_up_users WHERE guild_id = ? AND discord_id = ?", guildID, dID).Scan(&exists)
				if exists == 0 {
					_, _ = db.DB.Exec("INSERT INTO cleaned_up_users (guild_id, discord_id) VALUES (?, ?)", guildID, dID)
					actuallyCleanedUp = append(actuallyCleanedUp, dID)
					cleanedUpCount++
				}
			}

			// Clean up the tracking table for users who are currently in the server (in case they re-joined)
			for dID := range guildMemberMap {
				_, _ = db.DB.Exec("DELETE FROM cleaned_up_users WHERE guild_id = ? AND discord_id = ?", guildID, dID)
			}

			excludedMappingIDs := make(map[string]bool)
			exRows, err := db.DB.Query("SELECT discord_id FROM excluded_mappings WHERE guild_id = ?", guildID)
			if err == nil {
				for exRows.Next() {
					var dID string
					if exRows.Scan(&dID) == nil {
						excludedMappingIDs[dID] = true
					}
				}
				_ = exRows.Close()
			}

			var unmappedUsers []string
			for _, mem := range members {
				if mem.User != nil && !mem.User.Bot {
					if !mappedUserMap[mem.User.ID] && !excludedMappingIDs[mem.User.ID] {
						unmappedUsers = append(unmappedUsers, mem.User.ID)
					}
				}
			}

			type corpPair struct {
				cid int
				rid string
			}
			var corpPairs []corpPair
			corpRows, _ := db.DB.Query("SELECT corp_id, role_id FROM corp_to_role WHERE guild_id = ?", guildID)
			if corpRows != nil {
				for corpRows.Next() {
					var cp corpPair
					if corpRows.Scan(&cp.cid, &cp.rid) == nil {
						corpPairs = append(corpPairs, cp)
					}
				}
				_ = corpRows.Close()
			}
			var mappedCorps []string
			for _, cp := range corpPairs {
				mappedCorps = append(mappedCorps, fmt.Sprintf("- <@&%s> (Corp: %s, ID: %d)", cp.rid, roles.GetCorpName(cp.cid), cp.cid))
			}

			type alliancePair struct {
				aid int
				rid string
			}
			var alliancePairs []alliancePair
			allianceRows, _ := db.DB.Query("SELECT alliance_id, role_id FROM alliance_to_role WHERE guild_id = ?", guildID)
			if allianceRows != nil {
				for allianceRows.Next() {
					var ap alliancePair
					if allianceRows.Scan(&ap.aid, &ap.rid) == nil {
						alliancePairs = append(alliancePairs, ap)
					}
				}
				_ = allianceRows.Close()
			}
			var mappedAlliances []string
			for _, ap := range alliancePairs {
				mappedAlliances = append(mappedAlliances, fmt.Sprintf("- <@&%s> (Alliance: %s, ID: %d)", ap.rid, roles.GetAllianceName(ap.aid), ap.aid))
			}

			excludeRows, _ := db.DB.Query("SELECT discord_id FROM excluded_users WHERE guild_id = ?", guildID)
			var excludedUsers []string
			if excludeRows != nil {
				for excludeRows.Next() {
					var dID string
					if excludeRows.Scan(&dID) == nil {
						excludedUsers = append(excludedUsers, fmt.Sprintf("- <@%s>", dID))
					}
				}
				_ = excludeRows.Close()
			}

			excludeMapRows, _ := db.DB.Query("SELECT discord_id FROM excluded_mappings WHERE guild_id = ?", guildID)
			var excludedMapUsers []string
			if excludeMapRows != nil {
				for excludeMapRows.Next() {
					var dID string
					if excludeMapRows.Scan(&dID) == nil {
						excludedMapUsers = append(excludedMapUsers, fmt.Sprintf("- <@%s>", dID))
					}
				}
				_ = excludeMapRows.Close()
			}

			adminRows, _ := db.DB.Query("SELECT role_id FROM admin_roles WHERE guild_id = ?", guildID)
			var mappedAdmins []string
			if adminRows != nil {
				for adminRows.Next() {
					var rID string
					if adminRows.Scan(&rID) == nil {
						mappedAdmins = append(mappedAdmins, fmt.Sprintf("- <@&%s>", rID))
					}
				}
				_ = adminRows.Close()
			}

			greetOnRows, _ := db.DB.Query("SELECT role_id FROM greet_on_role WHERE guild_id = ?", guildID)
			var mappedGreets []string
			if greetOnRows != nil {
				for greetOnRows.Next() {
					var rID string
					if greetOnRows.Scan(&rID) == nil {
						mappedGreets = append(mappedGreets, fmt.Sprintf("- <@&%s>", rID))
					}
				}
				_ = greetOnRows.Close()
			}

			seatNeededRows, _ := db.DB.Query("SELECT role_id FROM seat_needed_roles WHERE guild_id = ?", guildID)
			var mappedSeatRoles []string
			if seatNeededRows != nil {
				for seatNeededRows.Next() {
					var rID string
					if seatNeededRows.Scan(&rID) == nil {
						mappedSeatRoles = append(mappedSeatRoles, fmt.Sprintf("- <@&%s>", rID))
					}
				}
				_ = seatNeededRows.Close()
			}

			allianceReqCorpRows, _ := db.DB.Query("SELECT role_id FROM alliance_required_corp_roles WHERE guild_id = ?", guildID)
			var mappedAllianceReqCorpRoles []string
			if allianceReqCorpRows != nil {
				for allianceReqCorpRows.Next() {
					var rID string
					if allianceReqCorpRows.Scan(&rID) == nil {
						mappedAllianceReqCorpRoles = append(mappedAllianceReqCorpRoles, fmt.Sprintf("- <@&%s>", rID))
					}
				}
				_ = allianceReqCorpRows.Close()
			}

			ex2FARows, _ := db.DB.Query("SELECT role_id FROM excluded_2fa_roles WHERE guild_id = ?", guildID)
			var mappedExcluded2FARoles []string
			if ex2FARows != nil {
				for ex2FARows.Next() {
					var rID string
					if ex2FARows.Scan(&rID) == nil {
						mappedExcluded2FARoles = append(mappedExcluded2FARoles, fmt.Sprintf("- <@&%s>", rID))
					}
				}
				_ = ex2FARows.Close()
			}

			standingRows, _ := db.DB.Query("SELECT standing_type, role_id FROM standing_to_role WHERE guild_id = ?", guildID)
			var mappedStandings []string
			if standingRows != nil {
				for standingRows.Next() {
					var stype string
					var rID string
					if standingRows.Scan(&stype, &rID) == nil {
						mappedStandings = append(mappedStandings, fmt.Sprintf("- **%s standing**: <@&%s>", strings.Title(stype), rID))
					}
				}
				_ = standingRows.Close()
			}

			var exCorpIDs []int
			exCorpRows, _ := db.DB.Query("SELECT corp_id FROM excluded_corp_standings WHERE guild_id = ?", guildID)
			if exCorpRows != nil {
				for exCorpRows.Next() {
					var cid int
					if exCorpRows.Scan(&cid) == nil {
						exCorpIDs = append(exCorpIDs, cid)
					}
				}
				_ = exCorpRows.Close()
			}
			var excludedCorpStandings []string
			for _, cid := range exCorpIDs {
				var rID string
				_ = db.DB.QueryRow("SELECT role_id FROM corp_to_role WHERE guild_id = ? AND corp_id = ?", guildID, cid).Scan(&rID)
				if rID != "" {
					excludedCorpStandings = append(excludedCorpStandings, fmt.Sprintf("- <@&%s> (%s, ID: %d)", rID, roles.GetCorpName(cid), cid))
				} else {
					excludedCorpStandings = append(excludedCorpStandings, fmt.Sprintf("- %s (ID: %d)", roles.GetCorpName(cid), cid))
				}
			}

			var postLogs, logChannel string
			_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'post_logs'", guildID).Scan(&postLogs)
			_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'log_channel'", guildID).Scan(&logChannel)
			if postLogs == "" {
				postLogs = "1"
			}
			logStatus := "disabled"
			if postLogs == "1" {
				logStatus = "enabled"
			}
			logChanDisplay := "Not set"
			if logChannel != "" {
				logChanDisplay = fmt.Sprintf("<#%s>", logChannel)
			}

			var mailChannel string
			_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'mail_channel'", guildID).Scan(&mailChannel)
			if mailChannel == "" {
				_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'evemail_channel'", guildID).Scan(&mailChannel)
			}
			mailChanDisplay := "disabled"
			if mailChannel != "" {
				mailChanDisplay = fmt.Sprintf("<#%s>", mailChannel)
			}

			var eventsChannel string
			_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'events_channel'", guildID).Scan(&eventsChannel)
			eventsChanDisplay := "disabled"
			if eventsChannel != "" {
				eventsChanDisplay = fmt.Sprintf("<#%s>", eventsChannel)
			}

			var autoMapChars string
			_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'auto_map_chars'", guildID).Scan(&autoMapChars)
			autoMapStatus := "disabled"
			if autoMapChars == "1" {
				autoMapStatus = "enabled"
			}

			var twoFAEnabled string
			_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = '2fa_enabled'", guildID).Scan(&twoFAEnabled)
			twoFAStatus := "disabled"
			if twoFAEnabled == "1" {
				twoFAStatus = "enabled"
			}

			var greetChannel, greetingMsg string
			_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'greeting_channel'", guildID).Scan(&greetChannel)
			_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'greeting_message'", guildID).Scan(&greetingMsg)
			greetStatus := "disabled"
			if greetChannel != "" && greetingMsg != "" {
				greetStatus = fmt.Sprintf("enabled in <#%s> with message: '%s'", greetChannel, greetingMsg)
			}

			var guestRole string
			_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'guest_role'", guildID).Scan(&guestRole)
			guestRoleDisplay := "disabled"
			if guestRole != "" {
				guestRoleDisplay = fmt.Sprintf("<@&%s>", guestRole)
			}

			var missingSeatRole string
			_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'missing_seat_role'", guildID).Scan(&missingSeatRole)
			missingSeatRoleDisplay := "disabled"
			if missingSeatRole != "" {
				missingSeatRoleDisplay = fmt.Sprintf("<@&%s>", missingSeatRole)
			}

			reportMsg := "**CNG Mapping and Server Sync Report**\n\n"
			reportMsg += fmt.Sprintf("**Role Logs Channel:** %s (Status: %s)\n", logChanDisplay, logStatus)
			reportMsg += fmt.Sprintf("**EVE Mail Channel:** %s\n", mailChanDisplay)
			reportMsg += fmt.Sprintf("**Calendar Events Channel:** %s\n", eventsChanDisplay)
			reportMsg += fmt.Sprintf("**Automatic Member Mapping:** %s\n", autoMapStatus)
			reportMsg += fmt.Sprintf("**Two-Factor Auth (2FA):** %s\n", twoFAStatus)
			reportMsg += fmt.Sprintf("**Join Greetings:** %s\n", greetStatus)
			reportMsg += fmt.Sprintf("**Guest Role Fallback:** %s\n", guestRoleDisplay)
			reportMsg += fmt.Sprintf("**Missing SeAT Role:** %s\n\n", missingSeatRoleDisplay)

			if len(unmappedUsers) > 0 {
				reportMsg += "**Unmapped Discord Users (Missing Mappings):**\n"
				for _, dID := range unmappedUsers {
					reportMsg += fmt.Sprintf("- <@%s>\n", dID)
				}
			}

			reportMsg += "\n**Mapped Corporation Roles:**\n"
			if len(mappedCorps) > 0 {
				for _, rLine := range mappedCorps {
					reportMsg += rLine + "\n"
				}
			} else {
				reportMsg += "_No Corporation roles are mapped._\n"
			}

			if len(mappedAlliances) > 0 {
				reportMsg += "\n**Mapped Alliance Roles:**\n"
				for _, rLine := range mappedAlliances {
					reportMsg += rLine + "\n"
				}
			}

			if len(excludedUsers) > 0 {
				reportMsg += "\n**Excluded Users (Skipped from role sync):**\n"
				for _, uLine := range excludedUsers {
					reportMsg += uLine + "\n"
				}
			}

			if len(excludedMapUsers) > 0 {
				reportMsg += "\n**Users Excluded from Automapping:**\n"
				for _, uLine := range excludedMapUsers {
					reportMsg += uLine + "\n"
				}
			}

			if len(mappedAdmins) > 0 {
				reportMsg += "\n**Mapped Admin Roles (Allowed to use bot commands):**\n"
				for _, aLine := range mappedAdmins {
					reportMsg += aLine + "\n"
				}
			}

			if len(mappedGreets) > 0 {
				reportMsg += "\n**Mapped Greeting-on-Assign Roles (Roles that trigger welcome greet message):**\n"
				for _, gLine := range mappedGreets {
					reportMsg += gLine + "\n"
				}
			}

			if len(mappedSeatRoles) > 0 {
				reportMsg += "\n**Mapped SeAT Required Roles (Roles requiring active SeAT registration):**\n"
				for _, srLine := range mappedSeatRoles {
					reportMsg += srLine + "\n"
				}
			}

			if len(mappedAllianceReqCorpRoles) > 0 {
				reportMsg += "\n**Mapped Alliance Roles Requiring Corporation Role:**\n"
				for _, arLine := range mappedAllianceReqCorpRoles {
					reportMsg += arLine + "\n"
				}
			}

			if len(mappedExcluded2FARoles) > 0 {
				reportMsg += "\n**Roles Excluded from 2FA Verification:**\n"
				for _, exLine := range mappedExcluded2FARoles {
					reportMsg += exLine + "\n"
				}
			}

			if len(mappedStandings) > 0 {
				reportMsg += "\n**Mapped EVE Standings Roles:**\n"
				for _, sLine := range mappedStandings {
					reportMsg += sLine + "\n"
				}
			}

			if len(excludedCorpStandings) > 0 {
				reportMsg += "\n**Corporations Excluded from Standings:**\n"
				for _, ecLine := range excludedCorpStandings {
					reportMsg += ecLine + "\n"
				}
			}

			if len(actuallyCleanedUp) > 0 {
				reportMsg += "\n**Cleaned up mappings from users who've left:**\n"
				for _, dID := range actuallyCleanedUp {
					reportMsg += fmt.Sprintf("- Discord ID: %s (Removed guild mapping state)\n", dID)
				}
				reportMsg += fmt.Sprintf("\n_Successfully removed %d obsolete mappings._\n", cleanedUpCount)
			}

			sendResponse(s, i.Interaction, reportMsg, true)
		}()

	case "help":
		helpMsg := "**Available Commands:**\n\n" +
			"__Mapping__\n" +
			"`/map_admin <role>` - Map a Discord role as a Bot Admin (Only allowed users can run commands).\n" +
			"`/map_char_id <character_id> <user>` - Manually map an EVE Character ID to a Discord User ID.\n" +
			"`/map_char <user>` - Auto-map a user based on their Discord nickname (must exactly match EVE name).\n" +
			"`/map_corp_id <corporation_id> <role>` - Manually map an EVE Corporation ID to a Discord Role ID.\n" +
			"`/map_corp <role>` - Auto-map a Discord role to an EVE Corporation ID matching its exact name.\n" +
			"`/map_alliance <alliance_id> <role>` - Map an EVE Alliance ID to a Discord Role ID.\n" +
			"`/map_greet_role <role>` - Map a Corporation or Alliance role to trigger a greeting message when auto-assigned.\n" +
			"`/seat_needed_roles <role>` - Toggle requiring active SeAT registration for a Corporation or Alliance role.\n" +
			"`/req_corp_for_alliance <role>` - Toggle requiring a mapped Corporation role for an Alliance role.\n\n" +
			"__Remove__\n" +
			"`/rm_corp <role>` - Remove an EVE Corporation mapping using its Discord Role.\n" +
			"`/rm_corp_id <id>` - Remove an EVE Corporation mapping using its EVE ID or Discord Role mention.\n" +
			"`/rm_alliance <role>` - Remove an EVE Alliance mapping using its Discord Role.\n" +
			"`/rm_char_id <id>` - Remove an EVE Character mapping using its EVE ID or Discord User mention.\n" +
			"`/exclude_sync <user>` - Exclude (or toggle) a Discord user from role synchronization checks.\n" +
			"`/exclude_map <user>` - Exclude (or toggle) a Discord user from automatic mapping and unmapped reporting.\n" +
			"`/exclude_corp_standing <role>` - Exclude (or toggle) a mapped Corporation role from receiving standing roles.\n" +
			"`/exclude_2fa <role>` - Exclude (or toggle) a mapped Corporation/Alliance role from requiring 2FA.\n\n" +
			"__Interactive__\n" +
			"`/report` - Generate a report of unmapped users and automatically prune users who left the server.\n" +
			"`/sync` - Force an immediate manual verification of all mapped users' roles.\n" +
			"`/char_info <user>` - Display detailed mapping, exclusion status, and automatically assigned roles for a Discord user.\n" +
			"`/toggle_automap` - Toggle automatic character mapping of new server members on join.\n" +
			"`/toggle_2fa` - Toggle requiring 2FA verification via EVE-mail before assigning mapped Discord roles.\n" +
			"`/set_greeting [channel] [message]` - Configure automatic greeting message for new members on join (omit options to disable).\n" +
			"`/set_guest [role]` - Set the guest role applied to mapped users whose Corp/Alliance is not yet mapped (omit option to disable).\n" +
			"`/set_missing_seat_role [role]` - Set a role to assign to users who need to log into SeAT but haven't (omit to disable).\n" +
			"`/map_standing_terrible [role]` - Set the role for Terrible standing (omit option to disable).\n" +
			"`/map_standing_bad [role]` - Set the role for Bad standing (omit option to disable).\n" +
			"`/map_standing_neutral [role]` - Set the role for Neutral standing (omit option to disable).\n" +
			"`/map_standing_good [role]` - Set the role for Good standing (omit option to disable).\n" +
			"`/map_standing_excellent [role]` - Set the role for Excellent standing (omit option to disable).\n" +
			"`/set_events_channel [channel]` - Set the channel where newly created calendar events will be posted (omit option to disable).\n" +
			"`/set_mail_channel [channel]` - Set the channel where EVE mails will be posted (omit option to disable).\n" +
			"`/set_donations_channel [channel]` - Set or disable the channel for donation announcements.\n" +
			"`/set_donations_track_corporation <role>` - Toggle tracking for a specific corporation via its mapped Discord role.\n" +
			"`/donations_leaderboard` - Displays the top 50 donors of all time to the tracked corporations.\n" +
			"`/post_donations_leaderboard` - Posts the leaderboard to the configured donations channel.\n" +
			"`/import_donations [data] [file] [role]` - Import pasted corporation wallet journal data to backfill donation history.\n" +
			"`/id <target>` - Output the Discord ID and mention format for a user, channel, role, or emoji.\n\n" +
			"`/courier_fee [volume] [jumps] [estimated price]` - Calculates the fee for a courier contract." +
			"__Debug__\n" +
			"`/toggle_logs` - Toggle posting role addition/removal logs to the designated channel.\n" +
			"`/set_log_channel <channel>` - Set the channel where logs should be posted."

		sendResponse(s, i.Interaction, helpMsg)

	case "exclude_sync":
		userOpt := getOption(data.Options, "user")
		if userOpt == nil {
			sendResponse(s, i.Interaction, "Usage: /exclude_sync user:<@User>")
			return
		}

		targetUser := userOpt.UserValue(s)
		if targetUser == nil {
			sendResponse(s, i.Interaction, "Invalid user specified.")
			return
		}
		discordID := targetUser.ID

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			var existing string
			err := db.DB.QueryRow("SELECT discord_id FROM excluded_users WHERE guild_id = ? AND discord_id = ?", guildID, discordID).Scan(&existing)
			if err == sql.ErrNoRows {
				_, err = db.DB.Exec("INSERT INTO excluded_users (guild_id, discord_id) VALUES (?, ?)", guildID, discordID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, fmt.Sprintf("<@%s> has been excluded from role sync checks.", discordID))
			} else if err != nil {
				return err
			} else {
				_, err = db.DB.Exec("DELETE FROM excluded_users WHERE guild_id = ? AND discord_id = ?", guildID, discordID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, fmt.Sprintf("<@%s> is no longer excluded from role sync checks.", discordID))
			}
			return nil
		})

	case "exclude_map":
		userOpt := getOption(data.Options, "user")
		if userOpt == nil {
			sendResponse(s, i.Interaction, "Usage: /exclude_map user:<@User>")
			return
		}

		targetUser := userOpt.UserValue(s)
		if targetUser == nil {
			sendResponse(s, i.Interaction, "Invalid user specified.")
			return
		}
		discordID := targetUser.ID

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			var existing string
			err := db.DB.QueryRow("SELECT discord_id FROM excluded_mappings WHERE guild_id = ? AND discord_id = ?", guildID, discordID).Scan(&existing)
			if err == sql.ErrNoRows {
				_, err = db.DB.Exec("INSERT INTO excluded_mappings (guild_id, discord_id) VALUES (?, ?)", guildID, discordID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, fmt.Sprintf("<@%s> has been excluded from automapping.", discordID))
			} else if err != nil {
				return err
			} else {
				_, err = db.DB.Exec("DELETE FROM excluded_mappings WHERE guild_id = ? AND discord_id = ?", guildID, discordID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, fmt.Sprintf("<@%s> is no longer excluded from automapping.", discordID))
			}
			return nil
		})

	case "exclude_corp_standing":
		roleOpt := getOption(data.Options, "role")
		if roleOpt == nil {
			sendResponse(s, i.Interaction, "Usage: /exclude_corp_standing role:<@Role>")
			return
		}

		var roleID string
		if r := roleOpt.RoleValue(s, i.GuildID); r != nil {
			roleID = r.ID
		} else if str, ok := roleOpt.Value.(string); ok {
			roleID = str
		}

		if roleID == "" {
			sendResponse(s, i.Interaction, "Invalid role specified.")
			return
		}

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			var corpID int
			err := db.DB.QueryRow("SELECT corp_id FROM corp_to_role WHERE guild_id = ? AND role_id = ?", guildID, roleID).Scan(&corpID)
			if err == sql.ErrNoRows {
				sendResponse(s, i.Interaction, fmt.Sprintf("Role <@&%s> is not mapped to any EVE Corporation.", roleID))
				return nil
			} else if err != nil {
				return err
			}

			var exists int
			err = db.DB.QueryRow("SELECT COUNT(*) FROM excluded_corp_standings WHERE guild_id = ? AND corp_id = ?", guildID, corpID).Scan(&exists)
			if err != nil {
				return err
			}

			if exists > 0 {
				_, err = db.DB.Exec("DELETE FROM excluded_corp_standings WHERE guild_id = ? AND corp_id = ?", guildID, corpID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, fmt.Sprintf("Corporation **%s** (<@&%s>) is no longer excluded from standings synchronization.", roles.GetCorpName(corpID), roleID))
			} else {
				_, err = db.DB.Exec("INSERT INTO excluded_corp_standings (guild_id, corp_id) VALUES (?, ?)", guildID, corpID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, fmt.Sprintf("Corporation **%s** (<@&%s>) is now excluded from standings synchronization. Any existing standing roles will be removed.", roles.GetCorpName(corpID), roleID))
			}
			go roles.CheckRoles(s)
			return nil
		})

	case "exclude_2fa":
		roleOpt := getOption(data.Options, "role")
		if roleOpt == nil {
			sendResponse(s, i.Interaction, "Usage: /exclude_2fa role:<@Role>")
			return
		}

		var roleID string
		if r := roleOpt.RoleValue(s, i.GuildID); r != nil {
			roleID = r.ID
		} else if str, ok := roleOpt.Value.(string); ok {
			roleID = str
		}

		if roleID == "" {
			sendResponse(s, i.Interaction, "Invalid role specified.")
			return
		}

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			var exists int
			err := db.DB.QueryRow("SELECT COUNT(*) FROM excluded_2fa_roles WHERE guild_id = ? AND role_id = ?", guildID, roleID).Scan(&exists)
			if err != nil {
				return err
			}

			if exists > 0 {
				_, err = db.DB.Exec("DELETE FROM excluded_2fa_roles WHERE guild_id = ? AND role_id = ?", guildID, roleID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, fmt.Sprintf("Role <@&%s> is no longer excluded from 2FA verification.", roleID))
			} else {
				_, err = db.DB.Exec("INSERT INTO excluded_2fa_roles (guild_id, role_id) VALUES (?, ?)", guildID, roleID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, fmt.Sprintf("Role <@&%s> is now excluded from 2FA verification. Members with this mapped role will bypass 2FA.", roleID))
			}
			go roles.CheckRoles(s)
			return nil
		})

	case "toggle_2fa":
		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			var val string
			err := db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = '2fa_enabled'", guildID).Scan(&val)
			if err == sql.ErrNoRows {
				val = "0"
			} else if err != nil {
				return err
			}
			newVal := "1"
			status := "enabled"
			if val == "1" {
				newVal = "0"
				status = "disabled"
			}
			_, err = db.DB.Exec("INSERT OR REPLACE INTO config (guild_id, key, value) VALUES (?, '2fa_enabled', ?)", guildID, newVal)
			if err != nil {
				return err
			}
			sendResponse(s, i.Interaction, fmt.Sprintf("Two Factor Authentication (2FA) is now %s.", status))
			go roles.CheckRoles(s)
			return nil
		})

	case "set_log_channel":
		chanOpt := getOption(data.Options, "channel")
		if chanOpt == nil {
			sendResponse(s, i.Interaction, "Usage: /set_log_channel channel:<#Channel>")
			return
		}

		var chanID string
		if ch := chanOpt.ChannelValue(s); ch != nil {
			chanID = ch.ID
		} else if str, ok := chanOpt.Value.(string); ok {
			chanID = str
		}

		if chanID == "" {
			sendResponse(s, i.Interaction, "Invalid channel specified.")
			return
		}

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			_, err := db.DB.Exec("INSERT OR REPLACE INTO config (guild_id, key, value) VALUES (?, 'log_channel', ?)", guildID, chanID)
			if err != nil {
				return err
			}
			sendResponse(s, i.Interaction, fmt.Sprintf("Log channel updated to <#%s>.", chanID))
			return nil
		})

	case "set_events_channel":
		chanOpt := getOption(data.Options, "channel")
		if chanOpt == nil {
			db.ExecuteOrQueue(s, i.ChannelID, func() error {
				_, err := db.DB.Exec("DELETE FROM config WHERE guild_id = ? AND key = 'events_channel'", guildID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, "Events posting is now disabled.")
				return nil
			})
			return
		}

		var chanID string
		if ch := chanOpt.ChannelValue(s); ch != nil {
			chanID = ch.ID
		} else if str, ok := chanOpt.Value.(string); ok {
			chanID = str
		}

		if chanID == "" {
			db.ExecuteOrQueue(s, i.ChannelID, func() error {
				_, err := db.DB.Exec("DELETE FROM config WHERE guild_id = ? AND key = 'events_channel'", guildID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, "Events posting is now disabled.")
				return nil
			})
			return
		}

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			_, err := db.DB.Exec("INSERT OR REPLACE INTO config (guild_id, key, value) VALUES (?, 'events_channel', ?)", guildID, chanID)
			if err != nil {
				return err
			}
			sendResponse(s, i.Interaction, fmt.Sprintf("Events channel updated to <#%s>. Newly created events will be posted here.", chanID))
			return nil
		})

	case "set_mail_channel", "set_evemail_channel":
		chanOpt := getOption(data.Options, "channel")
		if chanOpt == nil {
			db.ExecuteOrQueue(s, i.ChannelID, func() error {
				_, err := db.DB.Exec("DELETE FROM config WHERE guild_id = ? AND (key = 'mail_channel' OR key = 'evemail_channel')", guildID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, "EVE mail posting is now disabled.")
				return nil
			})
			return
		}

		var chanID string
		if ch := chanOpt.ChannelValue(s); ch != nil {
			chanID = ch.ID
		} else if str, ok := chanOpt.Value.(string); ok {
			chanID = str
		}

		if chanID == "" {
			db.ExecuteOrQueue(s, i.ChannelID, func() error {
				_, err := db.DB.Exec("DELETE FROM config WHERE guild_id = ? AND (key = 'mail_channel' OR key = 'evemail_channel')", guildID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, "EVE mail posting is now disabled.")
				return nil
			})
			return
		}

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			_, err := db.DB.Exec("INSERT OR REPLACE INTO config (guild_id, key, value) VALUES (?, 'mail_channel', ?)", guildID, chanID)
			if err != nil {
				return err
			}
			sendResponse(s, i.Interaction, fmt.Sprintf("EVE mail channel updated to <#%s>. Newly received EVE mails will be posted here.", chanID))
			return nil
		})

	case "toggle_logs":
		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			var val string
			err := db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'post_logs'", guildID).Scan(&val)
			if err == sql.ErrNoRows {
				val = "1"
			} else if err != nil {
				return err
			}
			newVal := "1"
			status := "enabled"
			if val == "1" {
				newVal = "0"
				status = "disabled"
			}
			_, err = db.DB.Exec("INSERT OR REPLACE INTO config (guild_id, key, value) VALUES (?, 'post_logs', ?)", guildID, newVal)
			if err != nil {
				return err
			}
			sendResponse(s, i.Interaction, fmt.Sprintf("Posting logs to channel is now %s.", status))
			return nil
		})

	case "toggle_automap":
		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			var val string
			err := db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'auto_map_chars'", guildID).Scan(&val)
			if err == sql.ErrNoRows {
				val = "0"
			} else if err != nil {
				return err
			}
			newVal := "1"
			status := "enabled"
			if val == "1" {
				newVal = "0"
				status = "disabled"
			}
			_, err = db.DB.Exec("INSERT OR REPLACE INTO config (guild_id, key, value) VALUES (?, 'auto_map_chars', ?)", guildID, newVal)
			if err != nil {
				return err
			}
			sendResponse(s, i.Interaction, fmt.Sprintf("Automatic character mapping is now %s.", status))
			return nil
		})

	case "set_greeting":
		chanOpt := getOption(data.Options, "channel")
		msgOpt := getOption(data.Options, "message")

		if chanOpt == nil || msgOpt == nil {
			db.ExecuteOrQueue(s, i.ChannelID, func() error {
				_, err := db.DB.Exec("DELETE FROM config WHERE guild_id = ? AND key = 'greeting_channel'", guildID)
				if err != nil {
					return err
				}
				_, err = db.DB.Exec("DELETE FROM config WHERE guild_id = ? AND key = 'greeting_message'", guildID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, "Greetings feature is now disabled.")
				return nil
			})
			return
		}

		var chanID string
		if ch := chanOpt.ChannelValue(s); ch != nil {
			chanID = ch.ID
		} else if str, ok := chanOpt.Value.(string); ok {
			chanID = str
		}

		greetingMsg := msgOpt.StringValue()

		if chanID == "" || greetingMsg == "" {
			db.ExecuteOrQueue(s, i.ChannelID, func() error {
				_, err := db.DB.Exec("DELETE FROM config WHERE guild_id = ? AND key = 'greeting_channel'", guildID)
				if err != nil {
					return err
				}
				_, err = db.DB.Exec("DELETE FROM config WHERE guild_id = ? AND key = 'greeting_message'", guildID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, "Greetings feature is now disabled.")
				return nil
			})
			return
		}

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			_, err := db.DB.Exec("INSERT OR REPLACE INTO config (guild_id, key, value) VALUES (?, 'greeting_channel', ?)", guildID, chanID)
			if err != nil {
				return err
			}
			_, err = db.DB.Exec("INSERT OR REPLACE INTO config (guild_id, key, value) VALUES (?, 'greeting_message', ?)", guildID, greetingMsg)
			if err != nil {
				return err
			}
			sendResponse(s, i.Interaction, fmt.Sprintf("Greetings configured! Channel: <#%s>, Message: '%s'", chanID, greetingMsg))
			return nil
		})

	case "set_guest":
		roleOpt := getOption(data.Options, "role")
		if roleOpt == nil {
			db.ExecuteOrQueue(s, i.ChannelID, func() error {
				_, err := db.DB.Exec("DELETE FROM config WHERE guild_id = ? AND key = 'guest_role'", guildID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, "Guest role feature is now disabled.")
				return nil
			})
			return
		}

		var roleID string
		if r := roleOpt.RoleValue(s, i.GuildID); r != nil {
			roleID = r.ID
		} else if str, ok := roleOpt.Value.(string); ok {
			roleID = str
		}

		if roleID == "" {
			db.ExecuteOrQueue(s, i.ChannelID, func() error {
				_, err := db.DB.Exec("DELETE FROM config WHERE guild_id = ? AND key = 'guest_role'", guildID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, "Guest role feature is now disabled.")
				return nil
			})
			return
		}

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			_, err := db.DB.Exec("INSERT OR REPLACE INTO config (guild_id, key, value) VALUES (?, 'guest_role', ?)", guildID, roleID)
			if err != nil {
				return err
			}
			sendResponse(s, i.Interaction, fmt.Sprintf("Guest role updated to <@&%s>.", roleID))
			go roles.CheckRoles(s)
			return nil
		})

	case "set_missing_seat_role":
		roleOpt := getOption(data.Options, "role")
		if roleOpt == nil {
			db.ExecuteOrQueue(s, i.ChannelID, func() error {
				_, err := db.DB.Exec("DELETE FROM config WHERE guild_id = ? AND key = 'missing_seat_role'", guildID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, "Missing SeAT role feature is now disabled.")
				return nil
			})
			return
		}

		var roleID string
		if r := roleOpt.RoleValue(s, i.GuildID); r != nil {
			roleID = r.ID
		} else if str, ok := roleOpt.Value.(string); ok {
			roleID = str
		}

		if roleID == "" {
			db.ExecuteOrQueue(s, i.ChannelID, func() error {
				_, err := db.DB.Exec("DELETE FROM config WHERE guild_id = ? AND key = 'missing_seat_role'", guildID)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, "Missing SeAT role feature is now disabled.")
				return nil
			})
			return
		}

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			_, err := db.DB.Exec("INSERT OR REPLACE INTO config (guild_id, key, value) VALUES (?, 'missing_seat_role', ?)", guildID, roleID)
			if err != nil {
				return err
			}
			sendResponse(s, i.Interaction, fmt.Sprintf("Missing SeAT role configured to <@&%s>.", roleID))
			go roles.CheckRoles(s)
			return nil
		})

	case "map_standing_terrible", "map_standing_bad", "map_standing_neutral", "map_standing_good", "map_standing_excellent":
		var stype string
		var sLabel string
		switch cmdName {
		case "map_standing_terrible":
			stype = roles.StandingTerrible
			sLabel = "Terrible"
		case "map_standing_bad":
			stype = roles.StandingBad
			sLabel = "Bad"
		case "map_standing_neutral":
			stype = roles.StandingNeutral
			sLabel = "Neutral"
		case "map_standing_good":
			stype = roles.StandingGood
			sLabel = "Good"
		case "map_standing_excellent":
			stype = roles.StandingExcellent
			sLabel = "Excellent"
		}

		roleOpt := getOption(data.Options, "role")
		if roleOpt == nil {
			db.ExecuteOrQueue(s, i.ChannelID, func() error {
				_, err := db.DB.Exec("DELETE FROM standing_to_role WHERE guild_id = ? AND standing_type = ?", guildID, stype)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, fmt.Sprintf("Standing role for **%s standing** has been removed.", sLabel))
				go roles.CheckRoles(s)
				return nil
			})
			return
		}

		var roleID string
		if r := roleOpt.RoleValue(s, i.GuildID); r != nil {
			roleID = r.ID
		} else if str, ok := roleOpt.Value.(string); ok {
			roleID = str
		}

		if roleID == "" {
			db.ExecuteOrQueue(s, i.ChannelID, func() error {
				_, err := db.DB.Exec("DELETE FROM standing_to_role WHERE guild_id = ? AND standing_type = ?", guildID, stype)
				if err != nil {
					return err
				}
				sendResponse(s, i.Interaction, fmt.Sprintf("Standing role for **%s standing** has been removed.", sLabel))
				go roles.CheckRoles(s)
				return nil
			})
			return
		}

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			_, err := db.DB.Exec("INSERT OR REPLACE INTO standing_to_role (guild_id, standing_type, role_id) VALUES (?, ?, ?)", guildID, stype, roleID)
			if err != nil {
				return err
			}
			sendResponse(s, i.Interaction, fmt.Sprintf("Standing role for **%s standing** set to <@&%s>.", sLabel, roleID))
			go roles.CheckRoles(s)
			return nil
		})

	case "char_info":
		userOpt := getOption(data.Options, "user")
		if userOpt == nil {
			sendResponse(s, i.Interaction, "Usage: /char_info user:<@User>")
			return
		}

		targetUser := userOpt.UserValue(s)
		if targetUser == nil {
			sendResponse(s, i.Interaction, "Invalid user specified.")
			return
		}
		targetUserID := targetUser.ID

		var eveID int
		err := db.DB.QueryRow("SELECT eve_id FROM character_to_discord WHERE discord_id = ?", targetUserID).Scan(&eveID)
		eveIDFound := err == nil

		var excludedSyncCount int
		_ = db.DB.QueryRow("SELECT COUNT(*) FROM excluded_users WHERE guild_id = ? AND discord_id = ?", guildID, targetUserID).Scan(&excludedSyncCount)
		excludedFromSync := excludedSyncCount > 0

		var excludedMapCount int
		_ = db.DB.QueryRow("SELECT COUNT(*) FROM excluded_mappings WHERE guild_id = ? AND discord_id = ?", guildID, targetUserID).Scan(&excludedMapCount)
		excludedFromMapping := excludedMapCount > 0

		var corpRoleMention = "None"
		var allianceRoleMention = "None"
		var standingRoleMention = "None"

		if eveIDFound {
			bodyBytes, errBytes := json.Marshal([]int{eveID})
			if errBytes == nil {
				resp, errPost := esi.Post("https://esi.evetech.net/latest/characters/affiliation/", "application/json", bytes.NewBuffer(bodyBytes))
				if errPost == nil && resp.StatusCode == 200 {
					var affiliations []roles.EsiCharacterAffiliation
					if json.NewDecoder(resp.Body).Decode(&affiliations) == nil && len(affiliations) > 0 {
						aff := affiliations[0]

						var corpRoleID string
						errCorp := db.DB.QueryRow("SELECT role_id FROM corp_to_role WHERE guild_id = ? AND corp_id = ?", guildID, aff.CorporationID).Scan(&corpRoleID)
						if errCorp == nil && corpRoleID != "" {
							corpRoleMention = fmt.Sprintf("<@&%s>", corpRoleID)

							// Check standing role
							var corpExcludedFromStandings int
							_ = db.DB.QueryRow("SELECT COUNT(*) FROM excluded_corp_standings WHERE guild_id = ? AND corp_id = ?", guildID, aff.CorporationID).Scan(&corpExcludedFromStandings)

							if corpExcludedFromStandings > 0 {
								standingRoleMention = "Excluded"
							} else {
								integ, _ := db.GetGuildIntegration(guildID)
								charIDStr := ""
								if integ.EveCharacterID > 0 {
									charIDStr = fmt.Sprintf("%d", integ.EveCharacterID)
								}
								botCorpID, errBotCorp := roles.FetchBotCorpID(charIDStr)
								if errBotCorp == nil {
									client := roles.GetOAuthHTTPClient(guildID)
									if client != nil {
										standings, errStandings := roles.FetchCorpStandings(client, charIDStr, botCorpID)
										standingVal := 0.0
										if errStandings == nil && standings != nil {
											standingVal = roles.ResolveStanding(standings, aff)
										}
										stype := roles.DetermineStandingType(standingVal)
										var sRoleID string
										errSRole := db.DB.QueryRow("SELECT role_id FROM standing_to_role WHERE guild_id = ? AND standing_type = ?", guildID, stype).Scan(&sRoleID)
										if errSRole == nil && sRoleID != "" {
											standingRoleMention = fmt.Sprintf("<@&%s> (%s standing, %.1f)", sRoleID, stype, standingVal)
										}
									}
								}
							}
						}

						var allianceRoleID string
						errAlliance := db.DB.QueryRow("SELECT role_id FROM alliance_to_role WHERE guild_id = ? AND alliance_id = ?", guildID, aff.AllianceID).Scan(&allianceRoleID)
						if errAlliance == nil && allianceRoleID != "" {
							allianceRoleMention = fmt.Sprintf("<@&%s>", allianceRoleID)
						}
					}
					_ = resp.Body.Close()
				} else if resp != nil && resp.Body != nil {
					_ = resp.Body.Close()
				}
			}
		}

		var eveLink string
		if eveIDFound {
			eveLink = fmt.Sprintf("https://evewho.com/character/%d", eveID)
		} else {
			eveLink = "Not Mapped"
		}

		syncExcl := "No"
		if excludedFromSync {
			syncExcl = "Yes"
		}

		mapExcl := "No"
		if excludedFromMapping {
			mapExcl = "Yes"
		}

		infoMsg := fmt.Sprintf("**Character Information for <@%s>**\n"+
			"- **Discord User ID Number:** `%s`\n"+
			"- **EVE Online Character Link:** %s\n"+
			"- **Automatically Mapped Roles:**\n"+
			"  - **Corporation Role:** %s\n"+
			"  - **Alliance Role:** %s\n"+
			"  - **Standing Role:** %s\n"+
			"- **Exclusions:**\n"+
			"  - **Excluded from Syncing:** `%s`\n"+
			"  - **Excluded from Mapping:** `%s`",
			targetUserID, targetUserID, eveLink, corpRoleMention, allianceRoleMention, standingRoleMention, syncExcl, mapExcl)

		sendResponse(s, i.Interaction, infoMsg)

	case "id":
		targetOpt := getOption(data.Options, "target")
		if targetOpt == nil {
			targetOpt = getOption(data.Options, "input")
		}
		if targetOpt == nil && len(data.Options) > 0 {
			targetOpt = data.Options[0]
		}
		if targetOpt == nil || strings.TrimSpace(targetOpt.StringValue()) == "" {
			sendResponse(s, i.Interaction, "Usage: `/id <@user | #channel | @role | :emoji:>`")
			return
		}

		targetStr := targetOpt.StringValue()
		resolvedID, formatted, err := ResolveDiscordID(s, i.GuildID, targetStr)
		if err != nil {
			sendResponse(s, i.Interaction, fmt.Sprintf("Could not resolve Discord ID for `%s`.", targetStr))
			return
		}

		sendResponse(s, i.Interaction, fmt.Sprintf("%s\n```%s```", resolvedID, formatted))

	case "set_donations_channel":
		chOpt := getOption(data.Options, "channel")
		if chOpt == nil {
			err := db.SetGuildDonationChannel(guildID, nil)
			if err != nil {
				sendResponse(s, i.Interaction, fmt.Sprintf("Error disabling donations channel: %v", err))
				return
			}
			sendResponse(s, i.Interaction, "Donations channel disabled.")
			return
		}
		ch := chOpt.ChannelValue(s)
		if ch == nil {
			sendResponse(s, i.Interaction, "Invalid channel specified.")
			return
		}
		chID := ch.ID
		err := db.SetGuildDonationChannel(guildID, &chID)
		if err != nil {
			sendResponse(s, i.Interaction, fmt.Sprintf("Error saving donations channel: %v", err))
			return
		}
		sendResponse(s, i.Interaction, fmt.Sprintf("Donations channel set to <#%s>.", chID))

	case "set_donations_track_corporation":
		roleOpt := getOption(data.Options, "role")
		if roleOpt == nil {
			sendResponse(s, i.Interaction, "Role is required.")
			return
		}
		role := roleOpt.RoleValue(s, guildID)
		if role == nil {
			sendResponse(s, i.Interaction, "Invalid role specified.")
			return
		}
		roleID := role.ID

		var eveCorpID int
		err := db.DB.QueryRow("SELECT corp_id FROM corp_to_role WHERE guild_id = ? AND role_id = ?", guildID, roleID).Scan(&eveCorpID)
		if err != nil {
			if err == sql.ErrNoRows {
				sendResponse(s, i.Interaction, fmt.Sprintf("Role <@&%s> is not mapped to an EVE Corporation. Please map it first using `/map_corp` or `/map_corp_id`.", roleID))
				return
			}
			sendResponse(s, i.Interaction, fmt.Sprintf("Database error: %v", err))
			return
		}

		tc, err := db.GetTrackedCorporation(guildID, roleID)
		if err != nil {
			sendResponse(s, i.Interaction, fmt.Sprintf("Database error: %v", err))
			return
		}

		corpDisplayName := donations.GetTrackedCorpDisplayName(guildID, roleID, eveCorpID, s)
		if tc != nil {
			_, err = db.RemoveTrackedCorporation(guildID, roleID)
			if err != nil {
				sendResponse(s, i.Interaction, fmt.Sprintf("Error removing tracked corporation: %v", err))
				return
			}
			sendResponse(s, i.Interaction, fmt.Sprintf("Stopped tracking donations for %s (role <@&%s>).", corpDisplayName, roleID))
		} else {
			err = db.AddTrackedCorporation(guildID, roleID, eveCorpID)
			if err != nil {
				sendResponse(s, i.Interaction, fmt.Sprintf("Error adding tracked corporation: %v", err))
				return
			}
			go donations.CheckDonations(s)
			sendResponse(s, i.Interaction, fmt.Sprintf("Started tracking donations for %s (role <@&%s>). Polling past and new donations...", corpDisplayName, roleID))
		}

	case "donations_leaderboard":
		leaderboardText, err := donations.BuildLeaderboard(guildID, s)
		if err != nil {
			sendResponse(s, i.Interaction, fmt.Sprintf("Error generating donations leaderboard: %v", err))
			return
		}
		sendResponse(s, i.Interaction, leaderboardText)

	case "post_donations_leaderboard":
		settings, err := db.GetGuildDonationSettings(guildID)
		if err != nil || settings == nil || settings.ChannelID == nil || *settings.ChannelID == "" {
			sendResponse(s, i.Interaction, "Donations channel is not configured. Use `/set_donations_channel` to configure it first.")
			return
		}
		leaderboardText, err := donations.BuildLeaderboard(guildID, s)
		if err != nil {
			sendResponse(s, i.Interaction, fmt.Sprintf("Error generating donations leaderboard: %v", err))
			return
		}
		chunks := splitMessage(leaderboardText, 2000)
		for _, chunk := range chunks {
			_, err = s.ChannelMessageSend(*settings.ChannelID, chunk)
			if err != nil {
				sendResponse(s, i.Interaction, fmt.Sprintf("Error posting leaderboard to channel: %v", err))
				return
			}
		}
		sendResponse(s, i.Interaction, fmt.Sprintf("Leaderboard posted to <#%s>.", *settings.ChannelID))

	case "import_donations":
		dataOpt := getOption(data.Options, "data")
		fileOpt := getOption(data.Options, "file")
		roleOpt := getOption(data.Options, "role")

		var rawText string
		if dataOpt != nil {
			rawText = dataOpt.StringValue()
		}

		if strings.TrimSpace(rawText) == "" && fileOpt != nil {
			attID, ok := fileOpt.Value.(string)
			if ok && data.Resolved != nil && data.Resolved.Attachments != nil {
				if att, exists := data.Resolved.Attachments[attID]; exists && att != nil && att.URL != "" {
					resp, err := http.Get(att.URL)
					if err == nil && resp != nil {
						body, _ := io.ReadAll(resp.Body)
						_ = resp.Body.Close()
						rawText = string(body)
					}
				}
			}
		}

		if strings.TrimSpace(rawText) == "" {
			sendResponse(s, i.Interaction, "You must provide pasted wallet journal text via the `data` option or upload a text file via the `file` option.")
			return
		}

		var targetCorpID int
		if roleOpt != nil {
			role := roleOpt.RoleValue(s, guildID)
			if role != nil {
				_ = db.DB.QueryRow("SELECT corp_id FROM corp_to_role WHERE guild_id = ? AND role_id = ?", guildID, role.ID).Scan(&targetCorpID)
			}
		}

		imported, dups, corpName, err := donations.ImportWalletJournal(guildID, targetCorpID, rawText)
		if err != nil {
			sendResponse(s, i.Interaction, fmt.Sprintf("Error importing donations: %v", err))
			return
		}

		msg := fmt.Sprintf("Successfully imported %d new donations (%d duplicates skipped) for corporation %s.", imported, dups, corpName)
		sendResponse(s, i.Interaction, donations.SanitizeText(msg))

	case "courier_fee":
		const baseFee float64 = 0.05
		const jumpFee float64 = 0.001

		var volume float64 = 0.0
		var jumps int64 = 0
		var estimate float64 = 0.0

		if opt := getOption(data.Options, "volume"); opt != nil && opt.FloatValue() > 0.0 {
				volume = opt.FloatValue()
		} else {
				sendResponse(s, i.Interaction, fmt.Sprintf("The volume must be provided and greater than 0.0"))
		}
		if opt := getOption(data.Options, "jumps"); opt != nil && opt.IntValue() > 0 {
				jumps = opt.IntValue()
		} else {
				sendResponse(s, i.Interaction, fmt.Sprintf("The jump counts must be provided and greater than 0"))
		}
		if opt := getOption(data.Options, "estimate"); opt != nil && opt.FloatValue() > 0.0 {
				estimate = opt.FloatValue()
		} else {
				sendResponse(s, i.Interaction, fmt.Sprintf("The estimate must be provided and greater than 0.0"))
		}

		var usingEstimate float64 = estimate * (baseFee + jumpFee * float64(jumps))
		var usingVolume float64 = (volume / 100) + baseFee + jumpFee * float64(jumps)

		finalFee := max(usingEstimate, usingVolume)

		parts := strings.Split(fmt.Sprintf("%.2f", finalFee), ".")
		intPart := parts[0]

		var result []byte
		for i := 0; i < len(intPart); i++ {
				if i > 0 && (len(intPart)-i)%3 == 0 {
						result = append(result, '\'')
				}
				result = append(result, intPart[i])
		}

		formattedISK := string(result) + "." + parts[1]

		sendResponse(s, i.Interaction, fmt.Sprintf("The calculated fee is %s ISK", formattedISK))
	}
}

var (
	userMentionRegex  = regexp.MustCompile(`<@!?(\d+)>`)
	roleMentionRegex  = regexp.MustCompile(`<@&(\d+)>`)
	chanMentionRegex  = regexp.MustCompile(`<#(\d+)>`)
	emojiMentionRegex = regexp.MustCompile(`<(a)?:([a-zA-Z0-9_~]+):(\d+)>`)
	snowflakeRegex    = regexp.MustCompile(`^\d{15,22}$`)
)

func ResolveDiscordID(s *discordgo.Session, guildID string, input string) (string, string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", "", fmt.Errorf("empty input")
	}

	// 1. Check custom emoji mention: <:name:id> or <a:name:id>
	if match := emojiMentionRegex.FindStringSubmatch(trimmed); len(match) == 4 {
		animated := match[1] == "a"
		name := match[2]
		id := match[3]
		var formatted string
		if animated {
			formatted = fmt.Sprintf("<a:%s:%s>", name, id)
		} else {
			formatted = fmt.Sprintf("<:%s:%s>", name, id)
		}
		return id, formatted, nil
	}

	// 2. Check role mention: <@&id>
	if match := roleMentionRegex.FindStringSubmatch(trimmed); len(match) == 2 {
		id := match[1]
		return id, fmt.Sprintf("<@&%s>", id), nil
	}

	// 3. Check user mention: <@id> or <@!id>
	if match := userMentionRegex.FindStringSubmatch(trimmed); len(match) == 2 {
		id := match[1]
		return id, fmt.Sprintf("<@%s>", id), nil
	}

	// 4. Check channel mention: <#id>
	if match := chanMentionRegex.FindStringSubmatch(trimmed); len(match) == 2 {
		id := match[1]
		return id, fmt.Sprintf("<#%s>", id), nil
	}

	// 5. Check raw snowflake numeric ID
	if snowflakeRegex.MatchString(trimmed) {
		id := trimmed
		if s != nil && guildID != "" {
			// Check roles
			if rolesList, err := s.GuildRoles(guildID); err == nil {
				for _, r := range rolesList {
					if r.ID == id {
						return id, fmt.Sprintf("<@&%s>", id), nil
					}
				}
			}
			// Check channels
			if chanList, err := s.GuildChannels(guildID); err == nil {
				for _, c := range chanList {
					if c.ID == id {
						return id, fmt.Sprintf("<#%s>", id), nil
					}
				}
			}
			// Check emojis
			if emojiList, err := s.GuildEmojis(guildID); err == nil {
				for _, e := range emojiList {
					if e.ID == id {
						if e.Animated {
							return id, fmt.Sprintf("<a:%s:%s>", e.Name, id), nil
						}
						return id, fmt.Sprintf("<:%s:%s>", e.Name, id), nil
					}
				}
			}
		}
		return id, fmt.Sprintf("<@%s>", id), nil
	}

	// 6. Check custom emoji by name :name: or name
	if s != nil && guildID != "" {
		emojiName := strings.Trim(trimmed, ":")
		if emojiList, err := s.GuildEmojis(guildID); err == nil {
			for _, e := range emojiList {
				if strings.EqualFold(e.Name, emojiName) {
					if e.Animated {
						return e.ID, fmt.Sprintf("<a:%s:%s>", e.Name, e.ID), nil
					}
					return e.ID, fmt.Sprintf("<:%s:%s>", e.Name, e.ID), nil
				}
			}
		}
	}

	// 7. Check channel by name #name or name
	if s != nil && guildID != "" {
		chanName := strings.TrimPrefix(trimmed, "#")
		if chanList, err := s.GuildChannels(guildID); err == nil {
			for _, c := range chanList {
				if strings.EqualFold(c.Name, chanName) {
					return c.ID, fmt.Sprintf("<#%s>", c.ID), nil
				}
			}
		}
	}

	// 8. Check role by name @name or name
	if s != nil && guildID != "" {
		roleName := strings.TrimPrefix(trimmed, "@")
		if rolesList, err := s.GuildRoles(guildID); err == nil {
			for _, r := range rolesList {
				if strings.EqualFold(r.Name, roleName) {
					return r.ID, fmt.Sprintf("<@&%s>", r.ID), nil
				}
			}
		}
	}

	// 9. Check member by @username, nickname, or global name
	if s != nil && guildID != "" {
		userName := strings.TrimPrefix(trimmed, "@")
		if members, err := s.GuildMembersSearch(guildID, userName, 10); err == nil && len(members) > 0 {
			for _, m := range members {
				if strings.EqualFold(m.Nick, userName) || (m.User != nil && (strings.EqualFold(m.User.Username, userName) || strings.EqualFold(m.User.GlobalName, userName))) {
					return m.User.ID, fmt.Sprintf("<@%s>", m.User.ID), nil
				}
			}
			if members[0].User != nil {
				return members[0].User.ID, fmt.Sprintf("<@%s>", members[0].User.ID), nil
			}
		}
		if members, err := s.GuildMembers(guildID, "", 1000); err == nil {
			for _, m := range members {
				if strings.EqualFold(m.Nick, userName) || (m.User != nil && (strings.EqualFold(m.User.Username, userName) || strings.EqualFold(m.User.GlobalName, userName))) {
					return m.User.ID, fmt.Sprintf("<@%s>", m.User.ID), nil
				}
			}
		}
	}

	return "", "", fmt.Errorf("could not resolve Discord ID for %q", input)
}

func StartAutoMapWorker(s *discordgo.Session) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		guilds := s.State.Guilds
		for _, guild := range guilds {
			guildID := guild.ID
			var val string
			err := db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'auto_map_chars'", guildID).Scan(&val)
			if err != nil || val != "1" {
				continue
			}

			members, err := s.GuildMembers(guildID, "", 1000)
			if err != nil {
				log.Printf("AutoMapWorker: Error fetching guild %s members: %v", guildID, err)
				continue
			}

			mappedIDs := make(map[string]bool)
			rows, err := db.DB.Query("SELECT discord_id FROM character_to_discord")
			if err == nil {
				for rows.Next() {
					var dID string
					if rows.Scan(&dID) == nil {
						mappedIDs[dID] = true
					}
				}
				_ = rows.Close()
			}

			excludedMappingIDs := make(map[string]bool)
			exRows, err := db.DB.Query("SELECT discord_id FROM excluded_mappings WHERE guild_id = ?", guildID)
			if err == nil {
				for exRows.Next() {
					var dID string
					if exRows.Scan(&dID) == nil {
						excludedMappingIDs[dID] = true
					}
				}
				_ = exRows.Close()
			}

			for _, member := range members {
				if member.User == nil || member.User.Bot {
					continue
				}

				discordID := member.User.ID
				if mappedIDs[discordID] {
					continue
				}

				if excludedMappingIDs[discordID] {
					continue
				}

				searchName := member.Nick
				if searchName == "" {
					if member.User.GlobalName != "" {
						searchName = member.User.GlobalName
					} else {
						searchName = member.User.Username
					}
				}

				var lastTestedName string
				err := db.DB.QueryRow("SELECT search_name FROM auto_map_attempts WHERE guild_id = ? AND discord_id = ?", guildID, discordID).Scan(&lastTestedName)
				if err == nil && lastTestedName == searchName {
					if len(member.Roles) == 0 {
						var guestRoleID string
						errGR := db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'guest_role'", guildID).Scan(&guestRoleID)
						if errGR == nil && guestRoleID != "" {
							errAdd := s.GuildMemberRoleAdd(guildID, discordID, guestRoleID)
							if errAdd != nil {
								db.DiscordLog(s, guildID, fmt.Sprintf("Failed to add Guest role <@&%s> to user <@%s>: %v", guestRoleID, discordID, errAdd))
							} else {
								db.DiscordLog(s, guildID, fmt.Sprintf("Added Guest role <@&%s> to user <@%s> because they could not be automatically mapped (cached).", guestRoleID, discordID))
							}
						}
					}
					continue
				}

				_, _ = db.DB.Exec("INSERT OR REPLACE INTO auto_map_attempts (guild_id, discord_id, search_name) VALUES (?, ?, ?)", guildID, discordID, searchName)

				namesArray := []string{searchName}
				bodyBytes, _ := json.Marshal(namesArray)
				resp, err := esi.Post("https://esi.evetech.net/latest/universe/ids/", "application/json", bytes.NewBuffer(bodyBytes))

				var found bool
				var eveID int
				if err == nil && resp.StatusCode == 200 {
					var universeResp struct {
						Characters []struct {
							ID   int    `json:"id"`
							Name string `json:"name"`
						} `json:"characters"`
					}
					if json.NewDecoder(resp.Body).Decode(&universeResp) == nil {
						if len(universeResp.Characters) > 0 {
							found = true
							eveID = universeResp.Characters[0].ID
							searchName = universeResp.Characters[0].Name
						}
					}
					_ = resp.Body.Close()
				} else if resp != nil && resp.Body != nil {
					_ = resp.Body.Close()
				}

				if !found {
					lowerSearchName := strings.ToLower(searchName)
					namesArray = []string{lowerSearchName}
					bodyBytes, _ = json.Marshal(namesArray)
					resp, err = esi.Post("https://esi.evetech.net/latest/universe/ids/", "application/json", bytes.NewBuffer(bodyBytes))
					if err == nil && resp.StatusCode == 200 {
						var universeResp struct {
							Characters []struct {
								ID   int    `json:"id"`
								Name string `json:"name"`
							} `json:"characters"`
						}
						if json.NewDecoder(resp.Body).Decode(&universeResp) == nil {
							if len(universeResp.Characters) > 0 {
								found = true
								eveID = universeResp.Characters[0].ID
								searchName = universeResp.Characters[0].Name
							}
						}
						_ = resp.Body.Close()
					} else if resp != nil && resp.Body != nil {
						_ = resp.Body.Close()
					}
				}

				if found {
					db.ExecuteOrQueue(s, "", func() error {
						_, err := db.DB.Exec("DELETE FROM character_to_discord WHERE discord_id = ?", discordID)
						if err != nil {
							return err
						}
						_, err = db.DB.Exec("INSERT OR REPLACE INTO character_to_discord (eve_id, discord_id) VALUES (?, ?)", eveID, discordID)
						if err != nil {
							return err
						}
						db.DiscordLog(s, guildID, fmt.Sprintf("Automatically mapped EVE Character '%s' (ID: %d - https://evewho.com/character/%d) to Discord User <@%s>", searchName, eveID, eveID, discordID))
						go roles.CheckRoles(s)
						return nil
					})
				} else {
					msg := fmt.Sprintf("Discord username %s could not be found as eve-online game character, unable to automatically map the discord user's roles.", searchName)
					db.DiscordLog(s, guildID, msg)

					db.ExecuteOrQueue(s, "", func() error {
						_, _ = db.DB.Exec("DELETE FROM character_to_discord WHERE discord_id = ?", discordID)
						roles.RemoveCorpAndAllianceRoles(s, guildID, member)
						return nil
					})

					if len(member.Roles) == 0 {
						var guestRoleID string
						err := db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'guest_role'", guildID).Scan(&guestRoleID)
						if err == nil && guestRoleID != "" {
							err = s.GuildMemberRoleAdd(guildID, discordID, guestRoleID)
							if err != nil {
								db.DiscordLog(s, guildID, fmt.Sprintf("Failed to add Guest role <@&%s> to user <@%s>: %v", guestRoleID, discordID, err))
							} else {
								db.DiscordLog(s, guildID, fmt.Sprintf("Added Guest role <@&%s> to user <@%s> because they could not be automatically mapped.", guestRoleID, discordID))
							}
						}
					}
				}
			}
		}
	}
}

func GuildMemberAdd(s *discordgo.Session, m *discordgo.GuildMemberAdd) {
	var greetingChannel, greetingMsg string
	errChan := db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'greeting_channel'", m.GuildID).Scan(&greetingChannel)
	errText := db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'greeting_message'", m.GuildID).Scan(&greetingMsg)

	if errChan == nil && errText == nil && greetingChannel != "" && greetingMsg != "" {
		if m.Member != nil && m.Member.User != nil {
			go func(userID string) {
				time.Sleep(45 * time.Second)
				msg := fmt.Sprintf("Welcome <@%s>! %s", userID, greetingMsg)
				_, _ = s.ChannelMessageSend(greetingChannel, msg)
			}(m.Member.User.ID)
		}
	}
}

func GuildScheduledEventCreate(s *discordgo.Session, event *discordgo.GuildScheduledEventCreate) {
	var eventsChannel string
	err := db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'events_channel'", event.GuildID).Scan(&eventsChannel)
	if err != nil || eventsChannel == "" {
		return
	}

	ev := event.GuildScheduledEvent
	var msgBuilder strings.Builder
	if emoji := config.GetEveCalendarEmoji(); emoji != "" {
		if strings.HasPrefix(emoji, "<") && strings.HasSuffix(emoji, ">") {
			msgBuilder.WriteString(emoji + "\n")
		} else {
			msgBuilder.WriteString(fmt.Sprintf("<:%s:%s>\n", emoji, emoji))
		}
	}
	fmt.Fprintf(&msgBuilder, "%s\n", ev.Name)
	if !ev.ScheduledStartTime.IsZero() {
		fmt.Fprintf(&msgBuilder, "> **Start at:** <t:%d:F> (<t:%d:R>)\n", ev.ScheduledStartTime.Unix(), ev.ScheduledStartTime.Unix())
	}

	if ev.GuildID != "" {
		fmt.Fprintf(&msgBuilder, "\n**Link to Event:** https://discord.com/events/%s/%s\n", ev.GuildID, ev.ID)
	}

	fullMsg := msgBuilder.String()
	runes := []rune(fullMsg)
	const maxDiscordLen = 2000

	var messagesToSend []string
	if len(runes) <= maxDiscordLen {
		messagesToSend = append(messagesToSend, fullMsg)
	} else {
		remaining := runes
		for len(remaining) > 0 {
			if len(remaining) > maxDiscordLen {
				messagesToSend = append(messagesToSend, string(remaining[:maxDiscordLen]))
				remaining = remaining[maxDiscordLen:]
			} else {
				messagesToSend = append(messagesToSend, string(remaining))
				break
			}
		}
	}

	for _, chunk := range messagesToSend {
		_, _ = s.ChannelMessageSend(eventsChannel, chunk)
	}

	ScheduleEventReminder(s, ev.GuildID, ev.ID, ev.Name, ev.ScheduledStartTime)
}

var (
	reminderMutex   sync.Mutex
	activeReminders = make(map[string]*time.Timer)
)

func ScheduleEventReminder(s *discordgo.Session, guildID, eventID, eventName string, startTime time.Time) {
	reminderMutex.Lock()
	defer reminderMutex.Unlock()

	if timer, exists := activeReminders[eventID]; exists {
		timer.Stop()
		delete(activeReminders, eventID)
	}

	reminderTime := startTime.Add(-30 * time.Minute)
	delay := time.Until(reminderTime)

	if delay <= 0 {
		log.Printf("Kickoff for event '%s' (ID: %s) is less than 30 minutes away or already passed. No reminder scheduled.", eventName, eventID)
		return
	}

	log.Printf("Scheduling kickoff reminder for event '%s' (ID: %s) in %v", eventName, eventID, delay)

	timer := time.AfterFunc(delay, func() {
		reminderMutex.Lock()
		delete(activeReminders, eventID)
		reminderMutex.Unlock()

		var eventsChannel string
		err := db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'events_channel'", guildID).Scan(&eventsChannel)
		if err != nil || eventsChannel == "" {
			return
		}

		ev, err := s.GuildScheduledEvent(guildID, eventID, false)
		if err != nil || ev == nil {
			log.Printf("Scheduled event '%s' (ID: %s) was deleted or not found. Skipping reminder.", eventName, eventID)
			return
		}

		if ev.Status == discordgo.GuildScheduledEventStatusCanceled || ev.Status == discordgo.GuildScheduledEventStatusCompleted {
			log.Printf("Scheduled event '%s' (ID: %s) was canceled or completed. Skipping reminder.", eventName, eventID)
			return
		}

		var msgBuilder strings.Builder
		if emoji := config.GetEveCalendarEmoji(); emoji != "" {
			if strings.HasPrefix(emoji, "<") && strings.HasSuffix(emoji, ">") {
				msgBuilder.WriteString(emoji + "\n")
			} else {
				msgBuilder.WriteString(fmt.Sprintf("<:%s:%s>\n", emoji, emoji))
			}
		}
		fmt.Fprintf(&msgBuilder, "**%s** starts in 30 minutes!**\n", ev.Name)
		if ev.GuildID != "" {
			fmt.Fprintf(&msgBuilder, "\n**Link to Event:** https://discord.com/events/%s/%s\n", ev.GuildID, ev.ID)
		}

		_, _ = s.ChannelMessageSend(eventsChannel, msgBuilder.String())
	})

	activeReminders[eventID] = timer
}

func InitExistingEventReminders(s *discordgo.Session) {
	go func() {
		time.Sleep(5 * time.Second)
		for _, g := range s.State.Guilds {
			events, err := s.GuildScheduledEvents(g.ID, false)
			if err != nil {
				log.Printf("Error fetching existing scheduled events for guild %s on startup: %v", g.ID, err)
				continue
			}

			for _, ev := range events {
				if ev.Status == discordgo.GuildScheduledEventStatusScheduled {
					ScheduleEventReminder(s, ev.GuildID, ev.ID, ev.Name, ev.ScheduledStartTime)
				}
			}
		}
	}()
}
