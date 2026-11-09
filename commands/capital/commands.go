package capital

import (
	"evemaildiscord/db"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// Commands returns the list of slash commands belonging to the fleet package.
func Commands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		{
			Name:                     "capital",
			Description:              "Capital Pilots related commands (Commands and FC only)",
			DefaultMemberPermissions: func(p int64) *int64 { return &p }(discordgo.PermissionViewChannel | discordgo.PermissionSendMessages),
			Options: []*discordgo.ApplicationCommandOption{
				{
					Name:        "add",
					Description: "Add a new capital pilots",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionUser,
							Name:        "user",
							Description: "The name of the pilot",
							Required:    true,
						},
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "ship",
							Description: "The ship flown",
							Required:    true,
						},
					},
				},
				{
					Name:        "remove",
					Description: "Removes a capital pilot",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionUser,
							Name:        "user",
							Description: "The name of the pilot",
							Required:    true,
						},
					},
				},
				{
					Name:        "ping",
					Description: "Delete an existing fleet template",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
				},
				{
					Name:        "list_all",
					Description: "List all capital pilots (regardless of active)",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
				},
			},
		},
	}
}

// HandleCommand processes interactions for capital-specific slash commands.
// It returns true if it handled the command, false otherwise.
func HandleCommand(s *discordgo.Session, i *discordgo.InteractionCreate, cmdName, guildID string, sendResponse func(*discordgo.Session, *discordgo.Interaction, string, ...bool)) bool {
	if cmdName != "capital" {
		return false
	}

	data := i.ApplicationCommandData()
	if len(data.Options) == 0 {
		return false
	}

	subCommand := data.Options[0]

	switch subCommand.Name {
	case "add":
		var user *discordgo.User
		var shipType string

		// Extract the options safely
		for _, opt := range subCommand.Options {
			if opt.Name == "user" {
				user = opt.UserValue(s)
			}
			if opt.Name == "ship" {
				if val, ok := opt.Value.(string); ok {
					shipType = val
				} else {
					shipType = opt.StringValue()
				}
			}
		}

		if user == nil {
			sendResponse(s, i.Interaction, "❌ Error: Could not resolve the selected user.")
			return true
		}

		pilotName := user.Username
		if user.GlobalName != "" {
			pilotName = user.GlobalName
		}

		// The DB's JOINs will attempt to resolve the Discord ID automatically if the pilot links their auth later.
		err := db.AddCapitalPilot(guildID, pilotName, 0, user.ID, shipType)
		if err != nil {
			sendResponse(s, i.Interaction, fmt.Sprintf("❌ Failed to add pilot: %v", err))
			return true
		}

		sendResponse(s, i.Interaction, fmt.Sprintf("✅ Capital pilot <@%s> flying a **%s** has been added.", user.ID, shipType))
		return true

	case "remove":
		var user *discordgo.User

		for _, opt := range subCommand.Options {
			if opt.Name == "user" {
				user = opt.UserValue(s)
			}
		}

		if user == nil {
			sendResponse(s, i.Interaction, "❌ Error: Could not resolve the selected user.")
			return true
		}

		pilotName := user.Username
		if user.GlobalName != "" {
			pilotName = user.GlobalName
		}

		err := db.RemoveCapitalPilot(guildID, pilotName)
		if err != nil {
			sendResponse(s, i.Interaction, fmt.Sprintf("❌ Failed to remove pilot: %v", err))
			return true
		}

		sendResponse(s, i.Interaction, fmt.Sprintf("✅ Capital pilot <@%s> has been removed.", user.ID))
		return true

	case "ping":
		pilots, err := db.GetActiveCapitalPilots(guildID)
		if err != nil {
			sendResponse(s, i.Interaction, fmt.Sprintf("❌ Failed to fetch capital pilots: %v", err))
			return true
		}

		if len(pilots) == 0 {
			sendResponse(s, i.Interaction, "There are currently no active capital pilots registered.")
			return true
		}

		var pings strings.Builder
		for _, p := range pilots {
			if p.DiscordID != "" {
				pings.WriteString(fmt.Sprintf("<@%s> (%s)\n", p.DiscordID, p.ShipName))
			} else {
				pings.WriteString(fmt.Sprintf("**%s** (%s)\n", p.PilotName, p.ShipName))
			}
		}

		sendResponse(s, i.Interaction, fmt.Sprintf("🔔 **Pinging all Capital Pilots:**\n\n%s", pings.String()))
		return true

	case "list_all":
		pilots, err := db.GetAllCapitalPilots(guildID)
		if err != nil {
			sendResponse(s, i.Interaction, fmt.Sprintf("❌ Failed to fetch capital pilots: %v", err))
			return true
		}

		if len(pilots) == 0 {
			sendResponse(s, i.Interaction, "There are currently no capital pilots registered.")
			return true
		}

		var activeList, inactiveList string
		for _, p := range pilots {
			status := "🟢"
			if !p.IsActive {
				status = "🔴"
			}

			// Format the name/mention
			mention := p.PilotName
			if p.DiscordID != "" {
				mention = fmt.Sprintf("<@%s>", p.DiscordID)
			}

			line := fmt.Sprintf("%s %s - **%s**\n", status, mention, p.ShipName)

			if p.IsActive {
				activeList += line
			} else {
				inactiveList += line
			}
		}

		// Compile the final message
		msg := "**Active Pilots:**\n"
		if activeList == "" {
			msg += "None\n"
		} else {
			msg += activeList
		}

		if inactiveList != "" {
			msg += "\n**Inactive Pilots:**\n" + inactiveList
		}

		// Truncate to Discord's 2000 character limit just in case
		if len(msg) > 2000 {
			msg = msg[:1996] + "..."
		}

		sendResponse(s, i.Interaction, msg)
		return true
	}

	return false
}
