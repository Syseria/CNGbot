package web

import (
	"embed"
	"html/template"
	"io"
)

//go:embed templates/*.html
var templateFS embed.FS

var (
	landingTemplate     = template.Must(template.ParseFS(templateFS, "templates/landing.html"))
	dashboardTemplate   = template.Must(template.ParseFS(templateFS, "templates/dashboard.html"))
	guildConfigTemplate = template.Must(template.ParseFS(templateFS, "templates/guild_config.html"))
)

type GuildCardView struct {
	DiscordGuildInfo
	EveLinked      bool
	SeatConfigured bool
}

type DashboardData struct {
	User         *UserSession
	Guilds       []GuildCardView
	MemberGuilds []DiscordGuildInfo
	BotInviteURL string
}

type TrackedCorpView struct {
	RoleID    string
	EveCorpID int
	CorpName  string
}

type GuildConfigData struct {
	User             *UserSession
	Guild            DiscordGuildInfo
	Integration      any
	EveCharacterName string
	TrackedCorps     []TrackedCorpView
	Config           struct {
		MailChannel   string
		LogChannel    string
		EventsChannel string
		GuestRole     string
		TwoFA         string
		AutoMap       string
		SyncRoles     string
		WandererDSN   string
	}
	Success string
	Error   string
}

func RenderLanding(w io.Writer) error {
	return landingTemplate.Execute(w, nil)
}

func RenderDashboard(w io.Writer, data DashboardData) error {
	return dashboardTemplate.Execute(w, data)
}

func RenderGuildConfig(w io.Writer, data GuildConfigData) error {
	return guildConfigTemplate.Execute(w, data)
}
