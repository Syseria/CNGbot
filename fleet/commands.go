package fleet

import (
	"encoding/json"
	"evemaildiscord/db"
	"evemaildiscord/esi"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// Commands returns the list of slash commands belonging to the fleet package.
func Commands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		{
			Name:                     "fleet_template",
			Description:              "Manage JSON templates for fleet structures (Admin only)",
			DefaultMemberPermissions: func(p int64) *int64 { return &p }(discordgo.PermissionManageChannels),
			Options: []*discordgo.ApplicationCommandOption{
				{
					Name:        "save",
					Description: "Save a new or existing fleet template",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "name",
							Description: "The name of the template (e.g. PVP, Mining)",
							Required:    true,
						},
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "json",
							Description: "The JSON structure of the template",
							Required:    true,
						},
					},
				},
				{
					Name:        "set_default",
					Description: "Set a saved template as the default for /fleet_build",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "name",
							Description: "The name of the saved template",
							Required:    true,
						},
					},
				},
				{
					Name:        "remove",
					Description: "Delete an existing fleet template",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Name:        "name",
							Description: "The name of the template to delete",
							Type:        discordgo.ApplicationCommandOptionString,
							Required:    true,
						},
					},
				},
			},
		},
		{
			Name:                     "fleet_build",
			Description:              "Structure your current EVE fleet based on a template (FC/WC only)",
			DefaultMemberPermissions: func(p int64) *int64 { return &p }(discordgo.PermissionManageChannels),
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "template",
					Description: "Optional: The name of the template to use (uses default if omitted)",
					Required:    false,
				},
			},
		},
		{
			Name:        "fleet_panel",
			Description: "Spawn the interactive fleet builder UI in this channel (Admin/CEO only)",
		},
	}
}

// HandleCommand processes interactions for fleet-specific slash commands.
// It returns true if it handled the command, false otherwise.
func HandleCommand(s *discordgo.Session, i *discordgo.InteractionCreate, cmdName, guildID string, sendResponse func(*discordgo.Session, *discordgo.Interaction, string, ...bool)) bool {
	data := i.ApplicationCommandData()

	switch cmdName {
	case "fleet_template":
		subCommand := data.Options[0]
		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			switch subCommand.Name {
			case "save":
				name := getOptionString(subCommand.Options, "name")
				jsonInput := getOptionString(subCommand.Options, "json")

				var tpl FleetTemplate
				if err := json.Unmarshal([]byte(jsonInput), &tpl); err != nil {
					sendResponse(s, i.Interaction, "Invalid JSON format. Please check your syntax.")
					return nil
				}
				tpl.Name = name

				if err := SaveFleetTemplate(guildID, tpl); err != nil {
					return err
				}

				sendResponse(s, i.Interaction, fmt.Sprintf("Template **%s** saved successfully.", name))
				go RefreshFleetPanel(s, guildID) // Trigger the live UI update

			case "set_default":
				name := getOptionString(subCommand.Options, "name")
				if _, err := GetFleetTemplate(guildID, name); err != nil {
					sendResponse(s, i.Interaction, fmt.Sprintf("Cannot set default: %v", err))
					return nil
				}

				_, err := db.DB.Exec("INSERT OR REPLACE INTO config (guild_id, key, value) VALUES (?, 'fleet_default_template', ?)", guildID, name)
				if err != nil {
					return err
				}

				sendResponse(s, i.Interaction, fmt.Sprintf("Template **%s** is now the default for this server.", name))
				go RefreshFleetPanel(s, guildID) // Trigger the live UI update

			case "remove":
				var templateName string

				for _, opt := range subCommand.Options {
					if opt.Name == "name" {
						templateName = opt.Value.(string) // Fallback for older discordgo versions
					}
				}

				if templateName == "" {
					return nil // Failsafe if the option wasn't found
				}
				guildID := i.GuildID

				// Prevent deletion of the default template
				var defaultName string
				_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'fleet_default_template'", guildID).Scan(&defaultName)
				if strings.EqualFold(templateName, defaultName) {
					err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
						Type: discordgo.InteractionResponseChannelMessageWithSource,
						Data: &discordgo.InteractionResponseData{
							Content: "⚠️ You cannot delete the default fleet template. Please set a different template as the default first.",
							Flags:   discordgo.MessageFlagsEphemeral,
						},
					})
					if err != nil {
						return err
					}
					return nil
				}

				err := DeleteFleetTemplate(guildID, templateName)
				if err != nil {
					err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
						Type: discordgo.InteractionResponseChannelMessageWithSource,
						Data: &discordgo.InteractionResponseData{
							Content: "❌ Failed to delete template: " + err.Error(),
							Flags:   discordgo.MessageFlagsEphemeral,
						},
					})
					if err != nil {
						return err
					}
					return nil
				}

				err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: fmt.Sprintf("✅ Template **%s** deleted successfully.", templateName),
						Flags:   discordgo.MessageFlagsEphemeral,
					},
				})
				if err != nil {
					return err
				}

				// Refresh the public UI panel to sync changes
				go RefreshFleetPanel(s, guildID)
			}
			return nil
		})
		return true

	case "fleet_build":
		var requestedName string
		for _, opt := range data.Options {
			if opt.Name == "template" {
				requestedName = opt.StringValue()
			}
		}

		db.ExecuteOrQueue(s, i.ChannelID, func() error {
			tpl, err := GetFleetTemplate(guildID, requestedName)
			if err != nil {
				sendResponse(s, i.Interaction, err.Error())
				return nil
			}

			integ, err := db.GetGuildIntegration(guildID)
			if err != nil || integ.EveCharacterID <= 0 {
				sendResponse(s, i.Interaction, "No EVE character linked for this server. Use /link first.")
				return nil
			}
			client := esi.GetOAuthHTTPClient(guildID)
			if client == nil {
				sendResponse(s, i.Interaction, "Failed to authenticate with EVE SSO. Try re-linking.")
				return nil
			}

			fleet, _, err := GetOwnFleet(client, integ.EveCharacterID)
			if err != nil {
				return err
			}
			if fleet.FleetID == 0 {
				sendResponse(s, i.Interaction, "You must be in a fleet to use this command.")
				return nil
			}
			if fleet.FleetBossID != int64(integ.EveCharacterID) {
				sendResponse(s, i.Interaction, "You must be the Fleet Boss to structure the fleet.")
				return nil
			}

			sendResponse(s, i.Interaction, fmt.Sprintf("Building fleet structure using template: **%s**. Please wait...", tpl.Name))

			sendResponse(s, i.Interaction, "Sweeping members into the first squad...")
			if err := MoveMembersToFirstSquad(client, fleet); err != nil {
				_, _ = s.ChannelMessageSend(i.ChannelID, fmt.Sprintf("⚠️ Members moved with errors: %v", err))
			}

			if err := BuildFleetFromTemplate(client, fleet.FleetID, tpl); err != nil {
				_, _ = s.ChannelMessageSend(i.ChannelID, fmt.Sprintf("⚠️ Warning: Fleet build encountered an error: %v", err))
				return nil
			}

			_, _ = s.ChannelMessageSend(i.ChannelID, "✅ Fleet structure aligned successfully.")
			return nil
		})
		return true

	case "fleet_panel":
		components, err := buildPanelComponents(guildID)
		if err != nil {
			sendResponse(s, i.Interaction, "No templates found. Create one with /fleet_template save first.")
			return true
		}

		msg, err := s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
			Embeds: []*discordgo.MessageEmbed{
				{
					Title:       "🚀 Fleet Operations Control",
					Description: "Select a layout from the dropdown and click Build to instantly structure your active EVE fleet.",
					Color:       0x0099ff,
				},
			},
			Components: components,
		})

		// Track the message so it can be auto-updated later
		if err == nil {
			_, err := db.DB.Exec("INSERT OR REPLACE INTO config (guild_id, key, value) VALUES (?, 'fleet_panel_channel', ?)", guildID, i.ChannelID)
			if err != nil {
				return false
			}
			_, err = db.DB.Exec("INSERT OR REPLACE INTO config (guild_id, key, value) VALUES (?, 'fleet_panel_message', ?)", guildID, msg.ID)
			if err != nil {
				return false
			}
		}

		sendResponse(s, i.Interaction, "Panel deployed successfully.")
		return true
	}
	return false
}

// Helper to safely extract string options
func getOptionString(options []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, opt := range options {
		if opt.Name == name {
			return opt.StringValue()
		}
	}
	return ""
}
