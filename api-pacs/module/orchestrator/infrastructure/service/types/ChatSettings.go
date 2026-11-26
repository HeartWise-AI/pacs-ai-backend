package types

// ChatSettingsResponse is what the viewer needs from the chat backend before the
// first message: the answer language when the user has chosen none.
type ChatSettingsResponse struct {
	// "fr" or "en" (CHAT_DEFAULT_LANGUAGE of the chat backend); null with an
	// older chat backend that does not report it.
	ChatDefaultLanguage *string `json:"chat_default_language"`
}
