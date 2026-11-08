package fleet

import (
	"fmt"
	"strings"

	"evemaildiscord/db"
	"evemaildiscord/esi"

	"github.com/bwmarrin/discordgo"
)

// buildPanelComponents dynamically generates the dropdown and button UI.
func buildPanelComponents(guildID string) ([]discordgo.MessageComponent, error) {
	templates, err := ListFleetTemplates(guildID)
	if err != nil || len(templates) == 0 {
		return nil, fmt.Errorf("no templates found")
	}

	var defaultName string
	_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'fleet_default_template'", guildID).Scan(&defaultName)

	var options []discordgo.SelectMenuOption
	var initialTemplate string

	for _, t := range templates {
		isDefault := strings.EqualFold(t, defaultName)
		if isDefault {
			initialTemplate = t
		}
		options = append(options, discordgo.SelectMenuOption{
			Label:   strings.ToUpper(t),
			Value:   t,
			Default: isDefault,
		})
	}

	if initialTemplate == "" && len(options) > 0 {
		options[0].Default = true
		initialTemplate = options[0].Value
	}

	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.SelectMenu{
					CustomID:    "select_fleet_template",
					Placeholder: "Select a fleet template...",
					Options:     options,
				},
			},
		},
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "Build Fleet",
					Style:    discordgo.SuccessButton,
					CustomID: "btn_build_fleet_" + initialTemplate,
					Disabled: false,
				},
			},
		},
	}, nil
}

// RefreshFleetPanel looks up the active panel in the DB and updates its components.
func RefreshFleetPanel(s *discordgo.Session, guildID string) {
	var channelID, messageID string
	_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'fleet_panel_channel'", guildID).Scan(&channelID)
	_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'fleet_panel_message'", guildID).Scan(&messageID)

	if channelID == "" || messageID == "" {
		return // No active panel is being tracked
	}

	components, err := buildPanelComponents(guildID)
	if err != nil {
		return
	}

	_, err = s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel:    channelID,
		ID:         messageID,
		Components: &components,
	})

	// If the edit fails (e.g., an Admin manually deleted the message in Discord), stop tracking it.
	if err != nil {
		_, err := db.DB.Exec("DELETE FROM config WHERE guild_id = ? AND key IN ('fleet_panel_channel', 'fleet_panel_message')", guildID)
		if err != nil {
			return
		}
	}
}

// RegisterUIHandlers hooks into Discord's event system to handle fleet dropdowns and buttons.
func RegisterUIHandlers(s *discordgo.Session) {
	s.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.Type != discordgo.InteractionMessageComponent {
			return
		}

		data := i.MessageComponentData()
		guildID := i.GuildID

		// 1. Dropdown Selection
		if data.CustomID == "select_fleet_template" {
			selectedTemplate := data.Values[0]
			msg := i.Message

			for rIdx, row := range msg.Components {
				actionRow := row.(*discordgo.ActionsRow)
				for cIdx, comp := range actionRow.Components {
					// Update the Button
					if btn, ok := comp.(*discordgo.Button); ok && strings.HasPrefix(btn.CustomID, "btn_build_fleet_") {
						btn.Disabled = false
						btn.CustomID = "btn_build_fleet_" + selectedTemplate
						actionRow.Components[cIdx] = btn
					}

					// Update the Dropdown Menu (Set the graphically active item)
					if menu, ok := comp.(*discordgo.SelectMenu); ok && menu.CustomID == "select_fleet_template" {
						for optIdx, opt := range menu.Options {
							menu.Options[optIdx].Default = (opt.Value == selectedTemplate)
						}
						actionRow.Components[cIdx] = menu
					}
				}
				msg.Components[rIdx] = actionRow
			}

			// Apply the update instantly while preserving the embed
			err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseUpdateMessage,
				Data: &discordgo.InteractionResponseData{
					Components: msg.Components,
					Embeds:     msg.Embeds, // <-- This preserves the Fleet Operations Control header
				},
			})
			if err != nil {
				return
			}
			return
		}

		// 2. Button Click
		if after, ok := strings.CutPrefix(data.CustomID, "btn_build_fleet_"); ok {
			templateName := after

			err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Flags: discordgo.MessageFlagsEphemeral,
				},
			})
			if err != nil {
				return
			}

			// Security Check: Ensure the clicker has native permissions in this channel
			if i.Member == nil || (i.Member.Permissions&(discordgo.PermissionManageChannels|discordgo.PermissionAdministrator)) == 0 {
				editInteraction(s, i.Interaction, "You do not have the required permissions (Manage Channels) to build a fleet from this panel.")
				return
			}

			tpl, err := GetFleetTemplate(guildID, templateName)
			if err != nil {
				editInteraction(s, i.Interaction, "Template error: "+err.Error())
				return
			}

			integ, err := db.GetGuildIntegration(guildID)
			if err != nil || integ.EveCharacterID <= 0 {
				editInteraction(s, i.Interaction, "No EVE character linked for this server. Use /link first.")
				return
			}

			client := esi.GetOAuthHTTPClient(guildID)
			if client == nil {
				editInteraction(s, i.Interaction, "Failed to authenticate with EVE SSO. Try re-linking.")
				return
			}

			fleetID, role, _, err := GetOwnFleet(client, integ.EveCharacterID)
			if err != nil || fleetID == 0 {
				editInteraction(s, i.Interaction, "You must be in an active EVE fleet to use this command.")
				return
			}
			if role != "fleet_commander" && role != "wing_commander" {
				editInteraction(s, i.Interaction, "You must be the Fleet Commander or Wing Commander in-game.")
				return
			}

			if err := BuildFleetFromTemplate(client, fleetID, tpl); err != nil {
				editInteraction(s, i.Interaction, fmt.Sprintf("⚠️ Warning: Fleet build encountered an error: %v", err))
				return
			}

			editInteraction(s, i.Interaction, fmt.Sprintf("✅ Fleet structure aligned to **%s** successfully.", tpl.Name))
		}
	})
}

// Helper to update the ephemeral loading state
func editInteraction(s *discordgo.Session, interaction *discordgo.Interaction, content string) {
	_, err := s.InteractionResponseEdit(interaction, &discordgo.WebhookEdit{
		Content: &content,
	})
	if err != nil {
		return
	}
}
