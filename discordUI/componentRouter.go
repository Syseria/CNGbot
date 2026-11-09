package discordui

import (
	"strings"

	"github.com/bwmarrin/discordgo"
)

// ComponentHandler defines the function signature for all UI callbacks.
type ComponentHandler func(s *discordgo.Session, i *discordgo.InteractionCreate)

var (
	exactHandlers  = make(map[string]ComponentHandler)
	prefixHandlers = make(map[string]ComponentHandler)
)

// RegisterExactMatch binds a specific CustomID to a handler function.
func RegisterExactMatch(customID string, handler ComponentHandler) {
	exactHandlers[customID] = handler
}

// RegisterPrefixMatch binds any CustomID starting with a prefix to a handler function.
func RegisterPrefixMatch(prefix string, handler ComponentHandler) {
	prefixHandlers[prefix] = handler
}

// StartRouter hooks the central multiplexer into the Discord session.
func StartRouter(s *discordgo.Session) {
	s.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		// Only process interactive UI elements
		if i.Type != discordgo.InteractionMessageComponent && i.Type != discordgo.InteractionModalSubmit {
			return
		}

		var customID string
		switch i.Type {
		case discordgo.InteractionMessageComponent:
			customID = i.MessageComponentData().CustomID
		case discordgo.InteractionModalSubmit:
			customID = i.ModalSubmitData().CustomID
		}

		if handler, exists := exactHandlers[customID]; exists {
			handler(s, i)
			return
		}

		// Fallback to Prefix Match Lookup
		for prefix, handler := range prefixHandlers {
			if strings.HasPrefix(customID, prefix) {
				handler(s, i)
				return
			}
		}
	})
}
