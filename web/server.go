package web

import (
	"log"
	"net/http"

	"evemaildiscord/config"

	"github.com/bwmarrin/discordgo"
)

func SetupRoutes(dg *discordgo.Session) *http.ServeMux {
	mux := http.NewServeMux()
	handler := NewWebHandler(dg)

	mux.HandleFunc("/", handler.HandleIndex)
	mux.HandleFunc("/dashboard", handler.HandleDashboard)
	mux.HandleFunc("/dashboard/guild", handler.HandleGuildConfig)
	mux.HandleFunc("/dashboard/guild/seat", handler.HandleGuildSeatSave)
	mux.HandleFunc("/dashboard/guild/wanderer", handler.HandleGuildWandererSave)
	mux.HandleFunc("/dashboard/guild/automation", handler.HandleGuildAutomationSave)
	mux.HandleFunc("/dashboard/guild/eve/unlink", handler.HandleGuildEveUnlink)
	mux.HandleFunc("/dashboard/guild/donations/import", handler.HandleGuildDonationsImport)

	mux.HandleFunc("/auth/discord/login", handler.HandleDiscordLogin)
	mux.HandleFunc("/auth/discord/callback", handler.HandleDiscordCallback)
	mux.HandleFunc("/auth/eve/login", handler.HandleEveLogin)
	mux.HandleFunc("/auth/eve/callback", handler.HandleEveCallback)
	mux.HandleFunc("/auth/logout", handler.HandleLogout)

	return mux
}

func StartServer(dg *discordgo.Session) {
	mux := SetupRoutes(dg)
	port := config.GetPort()
	addr := ":" + port

	log.Printf("Web Dashboard & OAuth2 server starting on http://localhost:%s", port)
	if err := http.ListenAndServe(addr, mux); err != nil && err != http.ErrServerClosed {
		log.Printf("Web server stopped: %v", err)
	}
}
