package main

import (
	"database/sql"
	discordui "evemaildiscord/discordUI"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	"evemaildiscord/commands"
	"evemaildiscord/config"
	"evemaildiscord/db"
	"evemaildiscord/donations"
	"evemaildiscord/fleet"
	"evemaildiscord/mail"
	"evemaildiscord/projects"
	"evemaildiscord/roles"
	"evemaildiscord/wanderer"
	"evemaildiscord/web"

	"github.com/bwmarrin/discordgo"
)

func getCommitHash() string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	if out, err := cmd.Output(); err == nil {
		hash := strings.TrimSpace(string(out))
		if hash != "" {
			return hash
		}
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				return setting.Value
			}
		}
	}
	return "unknown"
}

func main() {
	if err := db.InitDB(); err != nil {
		log.Fatalf("Error initializing database: %v", err)
	}
	defer func(DB *sql.DB) {
		err := DB.Close()
		if err != nil {

		}
	}(db.DB)

	dg, err := discordgo.New("Bot " + config.GetDiscordToken())
	if err != nil {
		log.Fatalf("Error creating Discord session: %v", err)
	}

	dg.AddHandler(commands.InteractionCreate)
	dg.AddHandler(commands.GuildMemberAdd)
	dg.AddHandler(commands.GuildScheduledEventCreate)
	dg.AddHandler(roles.HandleDirectMessage)
	dg.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentsGuildMembers | discordgo.IntentsGuildScheduledEvents | discordgo.IntentsDirectMessages

	// Let packages register their UI handlers
	fleet.InitUI()

	// Hook the generic router
	discordui.StartRouter(dg)

	err = dg.Open()
	if err != nil {
		log.Fatalf("Error opening Discord connection: %v", err)
	}
	defer func(dg *discordgo.Session) {
		err := dg.Close()
		if err != nil {

		}
	}(dg)
	log.Println("Discord Bot is running.")

	if err := commands.RegisterCommands(dg); err != nil {
		log.Printf("Failed to register slash commands: %v", err)
	}

	go func() {
		for _, guild := range dg.State.Guilds {
			guildID := guild.ID
			var adminRoles []string
			rows, err := db.DB.Query("SELECT role_id FROM admin_roles WHERE guild_id = ?", guildID)
			if err == nil {
				for rows.Next() {
					var r string
					if rows.Scan(&r) == nil {
						adminRoles = append(adminRoles, r)
					}
				}
				_ = rows.Close()
			}
			if len(adminRoles) > 0 {
				msg := fmt.Sprintf("Bot restarted (commit: %s). Currently accepting commands from the admin roles.", getCommitHash())
				db.DiscordLog(dg, guildID, msg)
			} else {
				msg := fmt.Sprintf("Bot restarted (commit: %s). No admin roles mapped! Any server member can execute the `/map_admin` command.", getCommitHash())
				db.DiscordLog(dg, guildID, msg)
			}
		}
	}()

	go mail.PollEveMail(dg)
	go projects.PollProjects(dg)
	go donations.PollDonations(dg)

	go roles.PollRoles(dg)
	go roles.PollGuestRoles(dg)
	go fleet.PollAutoInvite(dg)
	go wanderer.PollWanderer(dg)

	go web.StartServer(dg)

	go commands.StartAutoMapWorker(dg)

	commands.InitExistingEventReminders(dg)

	go db.ProcessQueue()

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc
}
