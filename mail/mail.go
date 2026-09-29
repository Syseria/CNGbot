package mail

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"evemaildiscord/config"
	"evemaildiscord/db"
	"evemaildiscord/esi"
	"evemaildiscord/parser"

	"github.com/bwmarrin/discordgo"
)

type EsiMailRecipient struct {
	RecipientID   int    `json:"recipient_id"`
	RecipientType string `json:"recipient_type"`
}

type EsiMailHeader struct {
	MailID     int                `json:"mail_id"`
	Subject    string             `json:"subject"`
	From       int                `json:"from"`
	Timestamp  string             `json:"timestamp"`
	Recipients []EsiMailRecipient `json:"recipients"`
}

type EsiMailBody struct {
	Body string `json:"body"`
}

var (
	reBr          = regexp.MustCompile(`(?i)<br\s*/?>`)
	reTags        = regexp.MustCompile(`(?i)</?[a-z][^>]*>`)
	reOpeningA    = regexp.MustCompile(`(?is)^<a(?:\s+[^>]*)?>$`)
	reClosingA    = regexp.MustCompile(`(?is)^</a\s*>$`)
	reHref        = regexp.MustCompile(`(?i)\bhref\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	reHttpScheme  = regexp.MustCompile(`(?i)^https?://`)
	reEmptyMDLink = regexp.MustCompile(`\[\s*\]\((https?://[^\s)]+)\)`)
)

func CleanEveMailBody(rawBody string) string {
	text := reBr.ReplaceAllString(rawBody, "\n")
	var aStack []string
	text = reTags.ReplaceAllStringFunc(text, func(tag string) string {
		if reOpeningA.MatchString(tag) {
			m := reHref.FindStringSubmatch(tag)
			var href string
			if len(m) > 0 {
				for i := 1; i < len(m); i++ {
					if m[i] != "" {
						href = strings.TrimSpace(m[i])
						break
					}
				}
			}
			if reHttpScheme.MatchString(href) {
				aStack = append(aStack, href)
				return "["
			}
			aStack = append(aStack, "")
			return ""
		}
		if reClosingA.MatchString(tag) {
			if len(aStack) > 0 {
				href := aStack[len(aStack)-1]
				aStack = aStack[:len(aStack)-1]
				if href != "" {
					return fmt.Sprintf("](%s)", href)
				}
			}
			return ""
		}
		return ""
	})
	for len(aStack) > 0 {
		href := aStack[len(aStack)-1]
		aStack = aStack[:len(aStack)-1]
		if href != "" {
			text += fmt.Sprintf("](%s)", href)
		}
	}
	text = reEmptyMDLink.ReplaceAllString(text, "[$1]($1)")
	text = html.UnescapeString(text)
	return strings.TrimSpace(text)
}

func PollEveMail(dg *discordgo.Session) {
	CheckMail(dg)
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		CheckMail(dg)
	}
}

type guildMailTarget struct {
	guildID string
	channel string
}

func CheckMail(dg *discordgo.Session) {
	if dg == nil || db.DB == nil {
		return
	}

	// 1. Collect all guilds with configured mail channels
	rows, err := db.DB.Query("SELECT guild_id, value FROM config WHERE key = 'mail_channel' OR key = 'evemail_channel'")
	if err != nil {
		log.Printf("Error querying mail channels: %v", err)
		return
	}

	var targets []guildMailTarget
	seenGuilds := make(map[string]bool)
	for rows.Next() {
		var gid, ch string
		if rows.Scan(&gid, &ch) == nil && ch != "" && !seenGuilds[gid] {
			seenGuilds[gid] = true
			targets = append(targets, guildMailTarget{guildID: gid, channel: ch})
		}
	}
	_ = rows.Close()

	if len(targets) == 0 {
		return
	}

	for _, target := range targets {
		processGuildMail(dg, target.guildID, target.channel)
	}
}

func processGuildMail(dg *discordgo.Session, guildID, channelID string) {
	integ, err := db.GetGuildIntegration(guildID)
	if err != nil {
		return
	}

	charID := integ.EveCharacterID
	if charID <= 0 {
		if envCharID := config.GetCharacterID(); envCharID != "" {
			charID, _ = strconv.Atoi(envCharID)
		}
	}
	if charID <= 0 {
		return
	}

	client := esi.GetOAuthHTTPClient(guildID)
	if client == nil {
		return
	}

	url := fmt.Sprintf("https://esi.evetech.net/v1/characters/%d/mail/", charID)
	resp, err := client.Get(url)
	if err != nil {
		log.Printf("[Guild %s] Error fetching mail headers: %v", guildID, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		log.Printf("[Guild %s] Error fetching mail headers: ESI returned status %d", guildID, resp.StatusCode)
		return
	}

	var mails []EsiMailHeader
	if err := json.NewDecoder(resp.Body).Decode(&mails); err != nil {
		log.Printf("[Guild %s] Error decoding mails: %v", guildID, err)
		return
	}

	if len(mails) == 0 {
		return
	}

	lastMailID, err := db.GetMailState(guildID)
	if err != nil {
		log.Printf("[Guild %s] Error reading mail state: %v", guildID, err)
		return
	}

	if lastMailID == 0 {
		_ = db.SetMailState(guildID, mails[0].MailID)
		log.Printf("[Guild %s] Baseline Mail ID set to %d. Waiting for NEW mail...", guildID, mails[0].MailID)
		return
	}

	for i := len(mails) - 1; i >= 0; i-- {
		mail := mails[i]
		if mail.MailID <= lastMailID {
			continue
		}

		log.Printf("[Guild %s] Found new mail! ID: %d, Subject: %s", guildID, mail.MailID, mail.Subject)

		bodyUrl := fmt.Sprintf("https://esi.evetech.net/v1/characters/%d/mail/%d/", charID, mail.MailID)
		bodyResp, err := client.Get(bodyUrl)
		if err != nil {
			log.Printf("[Guild %s] Failed to fetch body for mail %d: %v", guildID, mail.MailID, err)
			continue
		}

		if bodyResp.StatusCode != 200 {
			log.Printf("[Guild %s] Failed to fetch body for mail %d: ESI returned status %d", guildID, mail.MailID, bodyResp.StatusCode)
			_ = bodyResp.Body.Close()
			continue
		}

		var mailBody EsiMailBody
		if err := json.NewDecoder(bodyResp.Body).Decode(&mailBody); err != nil {
			log.Printf("[Guild %s] Error decoding mail body for mail %d: %v", guildID, mail.MailID, err)
			_ = bodyResp.Body.Close()
			continue
		}
		_ = bodyResp.Body.Close()

		cleanBody := CleanEveMailBody(mailBody.Body)
		sentAt, err := time.Parse(time.RFC3339, mail.Timestamp)
		if err != nil {
			sentAt, err = time.Parse("2006-01-02T15:04:05Z", mail.Timestamp)
			if err != nil {
				sentAt = time.Now().UTC()
			}
		}
		cleanBody = parser.InjectDiscordTimestamps(cleanBody, sentAt)
		bodyRunes := []rune(cleanBody)

		var discordID string
		_ = db.DB.QueryRow("SELECT discord_id FROM character_to_discord WHERE eve_id = ?", mail.From).Scan(&discordID)

		fromDisplay := fmt.Sprintf("Unknown EVE Character (ID: %d)", mail.From)
		if discordID != "" {
			fromDisplay = fmt.Sprintf("<@%s>", discordID)
		}

		var toRoles []string
		for _, rec := range mail.Recipients {
			var roleID string
			switch rec.RecipientType {
			case "corporation":
				_ = db.DB.QueryRow("SELECT role_id FROM corp_to_role WHERE guild_id = ? AND corp_id = ?", guildID, rec.RecipientID).Scan(&roleID)
			case "alliance":
				_ = db.DB.QueryRow("SELECT role_id FROM alliance_to_role WHERE guild_id = ? AND alliance_id = ?", guildID, rec.RecipientID).Scan(&roleID)
			}
			if roleID != "" {
				toRoles = append(toRoles, fmt.Sprintf("<@&%s>", roleID))
				break
			}
		}

		var toLine string
		if len(toRoles) > 0 {
			toLine = fmt.Sprintf("> **To:** %s\n", toRoles[0])
		} else {
			toLine = "> **To:** @here\n"
		}

		headers := fmt.Sprintf("> **Subject:** %s\n> **From:** %s\n%s\n", mail.Subject, fromDisplay, toLine)
		if emoji := config.GetEveMailEmoji(); emoji != "" {
			if strings.HasPrefix(emoji, "<") && strings.HasSuffix(emoji, ">") {
				headers = emoji + "\n" + headers
			} else {
				headers = fmt.Sprintf("<:%s:%s>\n", emoji, emoji) + headers
			}
		}


		const maxDiscordLen = 2000
		firstChunkSpace := maxDiscordLen - len([]rune(headers))

		var messagesToSend []string
		if len(bodyRunes) <= firstChunkSpace {
			messagesToSend = append(messagesToSend, headers+string(bodyRunes))
		} else {
			messagesToSend = append(messagesToSend, headers+string(bodyRunes[:firstChunkSpace]))
			remainingBody := bodyRunes[firstChunkSpace:]
			for len(remainingBody) > 0 {
				if len(remainingBody) > maxDiscordLen {
					messagesToSend = append(messagesToSend, string(remainingBody[:maxDiscordLen]))
					remainingBody = remainingBody[maxDiscordLen:]
				} else {
					messagesToSend = append(messagesToSend, string(remainingBody))
					break
				}
			}
		}

		for i, msgChunk := range messagesToSend {
			_, err = dg.ChannelMessageSend(channelID, msgChunk)
			if err != nil {
				db.DiscordLog(dg, guildID, fmt.Sprintf("Failed to send message chunk %d for mail %d to channel %s: %v", i+1, mail.MailID, channelID, err))
				break
			}
		}

		_ = db.SetMailState(guildID, mail.MailID)
		log.Printf("[Guild %s] Successfully processed mail %d.", guildID, mail.MailID)
	}
}

type SendMailRequest struct {
	ApprovedCost int                `json:"approved_cost,omitempty"`
	Body         string             `json:"body"`
	Recipients   []EsiMailRecipient `json:"recipients"`
	Subject      string             `json:"subject"`
}

func SendEveMail(client *http.Client, senderCharID int, recipientCharID int, subject, body string) error {
	if client == nil {
		return fmt.Errorf("oauth HTTP client is required")
	}

	charIDStr := strconv.Itoa(senderCharID)
	if senderCharID <= 0 {
		charIDStr = config.GetCharacterID()
	}
	if charIDStr == "" || charIDStr == "0" {
		return fmt.Errorf("sender character ID is not configured")
	}

	reqBody := SendMailRequest{
		Subject: subject,
		Body:    body,
		Recipients: []EsiMailRecipient{
			{
				RecipientID:   recipientCharID,
				RecipientType: "character",
			},
		},
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal mail request: %w", err)
	}

	url := fmt.Sprintf("https://esi.evetech.net/v1/characters/%s/mail/", charIDStr)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return fmt.Errorf("failed to create mail request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send mail via ESI: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		return fmt.Errorf("ESI mail send returned status %d", resp.StatusCode)
	}

	return nil
}
