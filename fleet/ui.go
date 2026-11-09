package fleet

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"evemaildiscord/db"
	"evemaildiscord/discordUI"
	"evemaildiscord/esi"

	"github.com/bwmarrin/discordgo"
)

// ==========================================
// STATE MANAGEMENT (Interactive Builder)
// ==========================================

var (
	draftMu sync.Mutex
	drafts  = make(map[string]*FleetTemplate) // Maps a User's Discord ID to their working draft
)

// ==========================================
// INITIALIZATION
// ==========================================

// InitUI registers all fleet-specific interactive UI elements with the master router.
func InitUI() {
	// Add Build Fleet Handle
	discordui.RegisterExactMatch("select_fleet_template", handleSelectTemplate)
	discordui.RegisterPrefixMatch("btn_build_fleet_", handleBuildFleet)

	// Add Create New Template Handle
	discordui.RegisterExactMatch("btn_draft_prompt_name", handleDraftPromptName)
	discordui.RegisterExactMatch("modal_draft_name", handleDraftInit)

	// Add Delete Template Handle
	discordui.RegisterPrefixMatch("btn_delete_fleet_", handleUIDeleteFleet)

	// Add Wing Handle
	discordui.RegisterExactMatch("btn_draft_add_wing", handleDraftAddWing)
	discordui.RegisterExactMatch("modal_draft_add_wing", handleModalAddWing)

	// Add Squad Handle
	discordui.RegisterExactMatch("btn_draft_add_squad", handleDraftPromptSquadWing)
	discordui.RegisterExactMatch("select_draft_wing_for_squad", handleDraftPromptSquadName)
	discordui.RegisterExactMatch("btn_draft_cancel_action", handleDraftRestoreUI)
	discordui.RegisterPrefixMatch("modal_draft_add_squad_", handleModalAddSquad)

	// Add Remove Item Handle
	discordui.RegisterExactMatch("btn_draft_remove_item", handleDraftPromptRemove)
	discordui.RegisterExactMatch("select_draft_remove_item", handleDraftRemoveAction)

	// Add Save & Publish Handle
	discordui.RegisterExactMatch("btn_draft_save", handleDraftSave)

	// Add Cancel Handle
	discordui.RegisterExactMatch("btn_draft_cancel", handleDraftCancelPrompt)
	discordui.RegisterExactMatch("btn_draft_cancel_confirm", handleDraftCancelConfirm)
	discordui.RegisterExactMatch("btn_draft_cancel_abort", handleDraftRestoreUI) // Re-uses the UI restore function
}

// ==========================================
// MAIN PANEL UI GENERATION
// ==========================================

// buildPanelComponents dynamically generates the dropdown and button UI.
func buildPanelComponents(guildID string) ([]discordgo.MessageComponent, error) {
	templates, err := ListFleetTemplates(guildID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	var defaultName string
	err = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'fleet_default_template'", guildID).Scan(&defaultName)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		defaultName = ""
	}

	var options []discordgo.SelectMenuOption
	var initialTemplate string

	if len(templates) > 0 {
		for _, t := range templates {
			isDefault := strings.EqualFold(t, defaultName)
			if isDefault {
				initialTemplate = t
			}

			label := strings.ToUpper(t)
			if isDefault {
				label += " (Default)"
			}

			options = append(options, discordgo.SelectMenuOption{
				Label:   label,
				Value:   t,
				Default: isDefault,
			})
		}

		if initialTemplate == "" && len(options) > 0 {
			options[0].Default = true
			initialTemplate = options[0].Value
		}
	} else {
		// Fallback if the database is completely empty
		options = append(options, discordgo.SelectMenuOption{
			Label:       "No templates available",
			Value:       "none",
			Description: "Create a new template to populate this list.",
		})
		initialTemplate = "none"
	}

	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.SelectMenu{
					CustomID:    "select_fleet_template",
					Placeholder: "Select a fleet template...",
					Options:     options,
					Disabled:    len(templates) == 0,
				},
			},
		},
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "Build Fleet",
					Style:    discordgo.SuccessButton,
					CustomID: "btn_build_fleet_" + initialTemplate,
					Disabled: len(templates) == 0,
				},
				discordgo.Button{
					Label:    "Create New Template",
					Style:    discordgo.PrimaryButton,
					CustomID: "btn_draft_prompt_name",
				},
				discordgo.Button{
					Label:    "Delete Template",
					Style:    discordgo.DangerButton,
					CustomID: "btn_delete_fleet_" + initialTemplate,
					Disabled: len(templates) == 0,
				},
			},
		},
	}, nil
}

// RefreshFleetPanel looks up the active panel in the DB and updates its components.
func RefreshFleetPanel(s *discordgo.Session, guildID string) {
	var channelID, messageID string
	errChan := db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'fleet_panel_channel'", guildID).Scan(&channelID)
	errMsg := db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'fleet_panel_message'", guildID).Scan(&messageID)

	if errors.Is(errChan, sql.ErrNoRows) || errors.Is(errMsg, sql.ErrNoRows) {
		return
	}
	if errChan != nil || errMsg != nil {
		return
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

	if err == nil {
		return
	}

	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) {
		if restErr.Response.StatusCode == http.StatusNotFound {
			_, _ = db.DB.Exec("DELETE FROM config WHERE guild_id = ? AND key IN ('fleet_panel_channel', 'fleet_panel_message')", guildID)
		}
	}
}

// ==========================================
// HANDLERS: MAIN PANEL
// ==========================================

func handleSelectTemplate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.MessageComponentData()
	if len(data.Values) == 0 {
		return
	}

	selectedTemplate := data.Values[0]
	msg := i.Message

	for rIdx, row := range msg.Components {
		actionRow, ok := row.(*discordgo.ActionsRow)
		if !ok {
			continue
		}

		for cIdx, comp := range actionRow.Components {
			if btn, ok := comp.(*discordgo.Button); ok {
				if strings.HasPrefix(btn.CustomID, "btn_build_fleet_") {
					btn.Disabled = false
					btn.CustomID = "btn_build_fleet_" + selectedTemplate
					actionRow.Components[cIdx] = btn
				} else if strings.HasPrefix(btn.CustomID, "btn_delete_fleet_") {
					btn.Disabled = false
					btn.CustomID = "btn_delete_fleet_" + selectedTemplate
					actionRow.Components[cIdx] = btn
				}
			}

			if menu, ok := comp.(*discordgo.SelectMenu); ok && menu.CustomID == "select_fleet_template" {
				for optIdx, opt := range menu.Options {
					menu.Options[optIdx].Default = opt.Value == selectedTemplate
				}
				actionRow.Components[cIdx] = menu
			}
		}
		msg.Components[rIdx] = actionRow
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Components: msg.Components,
			Embeds:     msg.Embeds,
		},
	})
}

func handleBuildFleet(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.MessageComponentData()
	templateName := strings.TrimPrefix(data.CustomID, "btn_build_fleet_")

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	guildID := i.GuildID
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

	fleet, _, err := GetOwnFleet(client, integ.EveCharacterID)
	if err != nil || fleet.FleetID == 0 {
		editInteraction(s, i.Interaction, "You must be in an active EVE fleet to use this command.")
		return
	}
	if fleet.FleetBossID != int64(integ.EveCharacterID) {
		editInteraction(s, i.Interaction, "You must be the Fleet Boss in-game.")
		return
	}

	if err := MoveMembersToFirstSquad(client, fleet); err != nil {
		editInteraction(s, i.Interaction, fmt.Sprintf("⚠️ Fleet built, but member sweep encountered an error: %v", err))
		return
	}

	if err := BuildFleetFromTemplate(client, fleet.FleetID, tpl); err != nil {
		editInteraction(s, i.Interaction, fmt.Sprintf("⚠️ Warning: Fleet build encountered an error: %v", err))
		return
	}

	editInteraction(s, i.Interaction, fmt.Sprintf("✅ Fleet structure aligned to **%s** successfully.", tpl.Name))
}

func handleUIDeleteFleet(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.MessageComponentData()
	templateName := strings.TrimPrefix(data.CustomID, "btn_delete_fleet_")

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	// Security Check
	if i.Member == nil || (i.Member.Permissions&(discordgo.PermissionManageChannels|discordgo.PermissionAdministrator)) == 0 {
		editInteraction(s, i.Interaction, "You do not have the required permissions to delete templates.")
		return
	}

	guildID := i.GuildID

	// Prevent deletion of the default template
	var defaultName string
	_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'fleet_default_template'", guildID).Scan(&defaultName)
	if strings.EqualFold(templateName, defaultName) {
		editInteraction(s, i.Interaction, "⚠️ You cannot delete the default fleet template. Please set a different template as the default first.")
		return
	}

	err := DeleteFleetTemplate(guildID, templateName)
	if err != nil {
		editInteraction(s, i.Interaction, "❌ Failed to delete template: "+err.Error())
		return
	}

	editInteraction(s, i.Interaction, fmt.Sprintf("✅ Template **%s** has been deleted.", templateName))

	// Refresh the panel so the deleted template is removed from the dropdown
	go RefreshFleetPanel(s, guildID)
}

// ==========================================
// HANDLERS: INTERACTIVE BUILDER
// ==========================================

func handleDraftPromptName(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Member == nil || (i.Member.Permissions&(discordgo.PermissionManageChannels|discordgo.PermissionAdministrator)) == 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "You do not have permission to create templates.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: "modal_draft_name",
			Title:    "Name Your Fleet Template",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "input_template_name",
							Label:       "Template Name (e.g. PVP, Mining)",
							Style:       discordgo.TextInputShort,
							Placeholder: "PVP Setup",
							Required:    true,
							MaxLength:   30,
						},
					},
				},
			},
		},
	})
}

func handleDraftInit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ModalSubmitData()

	// Safe extraction from the modal data
	var templateName string
	if len(data.Components) > 0 {
		if actionRow, ok := data.Components[0].(*discordgo.ActionsRow); ok && len(actionRow.Components) > 0 {
			if textInput, ok := actionRow.Components[0].(*discordgo.TextInput); ok {
				templateName = textInput.Value
			}
		}
	}

	if templateName == "" {
		templateName = "Unnamed Template"
	}

	userID := i.Member.User.ID

	// Initialise the draft in memory
	draftMu.Lock()
	drafts[userID] = &FleetTemplate{
		Name:  templateName,
		Wings: []WingTemplate{},
	}
	draft := drafts[userID]
	draftMu.Unlock()

	// Respond with the interactive builder embed
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:      discordgo.MessageFlagsEphemeral,
			Embeds:     []*discordgo.MessageEmbed{renderDraftEmbed(draft)},
			Components: renderDraftControls(),
		},
	})
}

func handleDraftAddWing(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: "modal_draft_add_wing",
			Title:    "Add New Wing",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "input_wing_name",
							Label:       "Wing Name",
							Style:       discordgo.TextInputShort,
							Placeholder: "e.g., DPS, Logi, Scouts",
							Required:    true,
							MaxLength:   20,
						},
					},
				},
			},
		},
	})
}

func handleModalAddWing(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ModalSubmitData()

	// Safe extraction
	var wingName string
	if len(data.Components) > 0 {
		if actionRow, ok := data.Components[0].(*discordgo.ActionsRow); ok && len(actionRow.Components) > 0 {
			if textInput, ok := actionRow.Components[0].(*discordgo.TextInput); ok {
				wingName = textInput.Value
			}
		}
	}

	if wingName == "" {
		wingName = "Unnamed Wing"
	}

	userID := i.Member.User.ID

	draftMu.Lock()
	draft, exists := drafts[userID]
	if exists {
		draft.Wings = append(draft.Wings, WingTemplate{Name: wingName, Squads: []string{}})
	}
	draftMu.Unlock()

	// If the draft expired (e.g., bot restarted mid-draft)
	if !exists {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Your draft session expired. Please start over.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	})

	// Panel update in the background
	embeds := []*discordgo.MessageEmbed{renderDraftEmbed(draft)}
	components := renderDraftControls()

	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds:     &embeds,
		Components: &components,
	})
}

func handleDraftPromptSquadWing(s *discordgo.Session, i *discordgo.InteractionCreate) {
	userID := i.Member.User.ID

	draftMu.Lock()
	draft, exists := drafts[userID]
	var wings []WingTemplate
	if exists {
		wings = draft.Wings
	}
	draftMu.Unlock()

	// Handle expired session or no wings created yet
	if !exists {
		editInteraction(s, i.Interaction, "Your draft session expired. Please start over.")
		return
	}
	if len(wings) == 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "⚠️ You must add at least one Wing before you can add a Squad.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Build dropdown options dynamically from the user's drafted wings
	var options []discordgo.SelectMenuOption
	for idx, w := range wings {
		options = append(options, discordgo.SelectMenuOption{
			Label: w.Name,
			Value: strconv.Itoa(idx),
		})
	}

	// Update the message to show the dropdown
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds: i.Message.Embeds,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.SelectMenu{
							CustomID:    "select_draft_wing_for_squad",
							Placeholder: "Select a Wing for the new Squad...",
							Options:     options,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{
							Label:    "Cancel",
							Style:    discordgo.SecondaryButton,
							CustomID: "btn_draft_cancel_action", // Reverts UI if they change their mind
						},
					},
				},
			},
		},
	})
}

func handleDraftPromptSquadName(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.MessageComponentData()
	if len(data.Values) == 0 {
		return
	}
	wingIndex := data.Values[0]

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: "modal_draft_add_squad_" + wingIndex,
			Title:    "Add New Squad",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "input_squad_name",
							Label:       "Squad Name",
							Style:       discordgo.TextInputShort,
							Placeholder: "e.g., Logi 1, Tackle, Snipers",
							Required:    true,
							MaxLength:   20,
						},
					},
				},
			},
		},
	})
}

func handleModalAddSquad(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ModalSubmitData()

	// Extract the target wing index from the Modal's CustomID
	wingIndexStr := strings.TrimPrefix(data.CustomID, "modal_draft_add_squad_")
	wingIdx, err := strconv.Atoi(wingIndexStr)
	if err != nil {
		return
	}

	var squadName string
	if len(data.Components) > 0 {
		if actionRow, ok := data.Components[0].(*discordgo.ActionsRow); ok && len(actionRow.Components) > 0 {
			if textInput, ok := actionRow.Components[0].(*discordgo.TextInput); ok {
				squadName = textInput.Value
			}
		}
	}

	if squadName == "" {
		squadName = "Unnamed Squad"
	}

	userID := i.Member.User.ID

	draftMu.Lock()
	draft, exists := drafts[userID]

	if exists && wingIdx >= 0 && wingIdx < len(draft.Wings) {
		draft.Wings[wingIdx].Squads = append(draft.Wings[wingIdx].Squads, squadName)
	}
	draftMu.Unlock()

	if !exists {
		editInteraction(s, i.Interaction, "Your draft session expired. Please start over.")
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	})

	// Panel update in the background
	embeds := []*discordgo.MessageEmbed{renderDraftEmbed(draft)}
	components := renderDraftControls()

	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds:     &embeds,
		Components: &components,
	})
}

func handleDraftRestoreUI(s *discordgo.Session, i *discordgo.InteractionCreate) {
	userID := i.Member.User.ID

	draftMu.Lock()
	draft, exists := drafts[userID]
	draftMu.Unlock()

	if !exists {
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{renderDraftEmbed(draft)},
			Components: renderDraftControls(),
		},
	})
}

func handleDraftPromptRemove(s *discordgo.Session, i *discordgo.InteractionCreate) {
	userID := i.Member.User.ID

	draftMu.Lock()
	draft, exists := drafts[userID]
	var wings []WingTemplate
	if exists {
		wings = draft.Wings
	}
	draftMu.Unlock()

	if !exists {
		editInteraction(s, i.Interaction, "Your draft session expired. Please start over.")
		return
	}

	if len(wings) == 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "⚠️ There are no items to remove.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	var options []discordgo.SelectMenuOption

	// Populate the dropdown with Wings and their respective Squads
	for wIdx, w := range wings {
		// Stop adding if we hit Discord's hard limit of 25 options per dropdown
		if len(options) >= 25 {
			break
		}

		options = append(options, discordgo.SelectMenuOption{
			Label:       fmt.Sprintf("Wing: %s", w.Name),
			Value:       fmt.Sprintf("wing_%d", wIdx),
			Description: "Deletes this wing and all its squads",
			Emoji:       &discordgo.ComponentEmoji{Name: "🛡️"},
		})

		for sIdx, sq := range w.Squads {
			if len(options) >= 25 {
				break
			}
			options = append(options, discordgo.SelectMenuOption{
				Label:       fmt.Sprintf("Squad: %s", sq),
				Value:       fmt.Sprintf("squad_%d_%d", wIdx, sIdx),
				Description: fmt.Sprintf("From Wing: %s", w.Name),
				Emoji:       &discordgo.ComponentEmoji{Name: "🚀"},
			})
		}
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds: i.Message.Embeds,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.SelectMenu{
							CustomID:    "select_draft_remove_item",
							Placeholder: "Select an item to delete...",
							Options:     options,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{
							Label:    "Cancel",
							Style:    discordgo.SecondaryButton,
							CustomID: "btn_draft_cancel_action", // Re-uses the cancel handler from Add Squad
						},
					},
				},
			},
		},
	})
}

func handleDraftRemoveAction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.MessageComponentData()
	if len(data.Values) == 0 {
		return
	}
	selectedValue := data.Values[0]

	userID := i.Member.User.ID

	draftMu.Lock()
	draft, exists := drafts[userID]
	if exists {
		if after, ok := strings.CutPrefix(selectedValue, "wing_"); ok {
			// Extract wing index and remove the wing
			idxStr := after
			wIdx, err := strconv.Atoi(idxStr)
			if err == nil && wIdx >= 0 && wIdx < len(draft.Wings) {
				draft.Wings = append(draft.Wings[:wIdx], draft.Wings[wIdx+1:]...)
			}
		} else if after0, ok0 := strings.CutPrefix(selectedValue, "squad_"); ok0 {
			// Extract wing and squad indices and remove the squad
			parts := strings.Split(after0, "_")
			if len(parts) == 2 {
				wIdx, err1 := strconv.Atoi(parts[0])
				sIdx, err2 := strconv.Atoi(parts[1])

				if err1 == nil && err2 == nil && wIdx >= 0 && wIdx < len(draft.Wings) {
					if sIdx >= 0 && sIdx < len(draft.Wings[wIdx].Squads) {
						draft.Wings[wIdx].Squads = append(draft.Wings[wIdx].Squads[:sIdx], draft.Wings[wIdx].Squads[sIdx+1:]...)
					}
				}
			}
		}
	}
	draftMu.Unlock()

	if !exists {
		editInteraction(s, i.Interaction, "Your draft session expired. Please start over.")
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{renderDraftEmbed(draft)},
			Components: renderDraftControls(),
		},
	})
}

func handleDraftSave(s *discordgo.Session, i *discordgo.InteractionCreate) {
	userID := i.Member.User.ID
	guildID := i.GuildID

	draftMu.Lock()
	draft, exists := drafts[userID]
	draftMu.Unlock()

	if !exists {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Your draft session expired. Please start over.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Validation: Prevent saving empty templates
	if len(draft.Wings) == 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "⚠️ You cannot save an empty template. Please add at least one Wing first.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Save to DB
	err := SaveFleetTemplate(guildID, *draft)
	if err != nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Failed to save template to database: " + err.Error(),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Clean up memory draft
	draftMu.Lock()
	delete(drafts, userID)
	draftMu.Unlock()

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{
				{
					Title:       "✅ Template Published",
					Description: fmt.Sprintf("The fleet template **%s** has been saved and is now available.", draft.Name),
					Color:       0x00ff00, // Green
				},
			},
			Components: []discordgo.MessageComponent{},
		},
	})

	// Asynchronously refresh the public channel panel so the new template appears in the dropdown
	go RefreshFleetPanel(s, guildID)
}

func handleDraftCancelPrompt(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds: i.Message.Embeds, // Keep the draft visualiser visible
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{
							Label:    "Yes, Discard Draft",
							Style:    discordgo.DangerButton,
							CustomID: "btn_draft_cancel_confirm",
						},
						discordgo.Button{
							Label:    "No, Keep Editing",
							Style:    discordgo.SecondaryButton,
							CustomID: "btn_draft_cancel_abort", // Restores the main buttons
						},
					},
				},
			},
		},
	})
}

func handleDraftCancelConfirm(s *discordgo.Session, i *discordgo.InteractionCreate) {
	userID := i.Member.User.ID

	// Remove the draft from memory
	draftMu.Lock()
	delete(drafts, userID)
	draftMu.Unlock()

	// Replace the embed with a cancellation notice and remove all buttons
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{
				{
					Title:       "🛑 Draft Cancelled",
					Description: "Your fleet template draft has been discarded.",
					Color:       0xff0000, // Red
				},
			},
			Components: []discordgo.MessageComponent{}, // Passing an empty slice removes all buttons
		},
	})
}

// ==========================================
// HELPER FUNCTIONS (Visuals)
// ==========================================

func renderDraftEmbed(tpl *FleetTemplate) *discordgo.MessageEmbed {
	desc := ""
	if len(tpl.Wings) == 0 {
		desc = "*No wings added yet. Click 'Add Wing' below to begin.*"
	} else {
		for _, w := range tpl.Wings {
			desc += fmt.Sprintf("🛡️ **Wing:** %s\n", w.Name)
			if len(w.Squads) == 0 {
				desc += " └─ *(No squads)*\n"
			}
			for idx, sq := range w.Squads {
				if idx == len(w.Squads)-1 {
					desc += fmt.Sprintf(" └─ Squad: %s\n", sq)
				} else {
					desc += fmt.Sprintf(" ├─ Squad: %s\n", sq)
				}
			}
			desc += "\n"
		}
	}

	return &discordgo.MessageEmbed{
		Title:       "🛠️ Template Builder: " + tpl.Name,
		Description: desc,
		Color:       0x00ff00, // Green
		Footer:      &discordgo.MessageEmbedFooter{Text: "Changes are not saved until you click Save & Publish."},
	}
}

func renderDraftControls() []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "Add Wing",
					Style:    discordgo.PrimaryButton,
					CustomID: "btn_draft_add_wing",
				},
				discordgo.Button{
					Label:    "Add Squad",
					Style:    discordgo.SecondaryButton,
					CustomID: "btn_draft_add_squad",
				},
				discordgo.Button{
					Label:    "Remove Item",
					Style:    discordgo.SecondaryButton,
					CustomID: "btn_draft_remove_item",
				},
			},
		},
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "Save & Publish",
					Style:    discordgo.SuccessButton,
					CustomID: "btn_draft_save",
				},
				discordgo.Button{
					Label:    "Cancel",
					Style:    discordgo.DangerButton,
					CustomID: "btn_draft_cancel",
				},
			},
		},
	}
}

func editInteraction(s *discordgo.Session, interaction *discordgo.Interaction, content string) {
	_, _ = s.InteractionResponseEdit(interaction, &discordgo.WebhookEdit{
		Content: &content,
	})
}
