package evalengine

import (
	"encoding/json"
	"log"
	"os/exec"
	"strconv"
	"strings"
)

// Sender represents a recent iMessage sender / contact.
type Sender struct {
	ID       string // phone number or identifier
	Label    string // contact name or phone
	Nickname string // preview or hint
}

// getRecentSenders returns the N most recent iMessage senders via the imsg CLI.
// On error (permission, imsg unreachable), returns an empty list.
func getRecentSenders(n int) []Sender {
	if n < 1 {
		n = 5
	}

	cmd := exec.Command("imsg", "chats", "--limit", strconv.Itoa(n), "--json")
	out, err := cmd.Output()
	if err != nil {
		// imsg may not have access permissions or may not be installed.
		// Log and return empty — the UI will show a blank picker.
		log.Printf("imsg chats failed: %v", err)
		return []Sender{}
	}

	var chats []struct {
		ID            string `json:"id"`
		DisplayName   string `json:"displayName"`
		PreviewText   string `json:"previewText"`
		LastMessageAt string `json:"lastMessageAt"`
	}
	if err := json.Unmarshal(out, &chats); err != nil {
		log.Printf("imsg chats JSON parse failed: %v", err)
		return []Sender{}
	}

	senders := make([]Sender, 0, len(chats))
	for _, c := range chats {
		label := c.DisplayName
		if label == "" {
			label = c.ID
		}
		nickname := c.PreviewText
		if len(nickname) > 60 {
			nickname = nickname[:60] + "…"
		}
		senders = append(senders, Sender{
			ID:       c.ID,
			Label:    label,
			Nickname: nickname,
		})
	}
	return senders
}

// buildPickerSenders converts a Sender list to PickerSender[] with 1-based indexing.
// Mirrors buildPickerChannels for the sender picker.
func buildPickerSenders(senders []Sender) []PickerSender {
	pickers := make([]PickerSender, 0, len(senders))
	for i, s := range senders {
		pickers = append(pickers, PickerSender{
			Index:    i + 1,
			ID:       s.ID,
			Label:    s.Label,
			Nickname: s.Nickname,
		})
	}
	return pickers
}

// resolveSenderSelection resolves a spoken utterance to a sender ID (or cancel).
// Rules mirror resolveChannelSelection: number, exact match, substring, cancel words.
func resolveSenderSelection(spoken string, senders []Sender) (id string, cancel bool, ok bool) {
	q := strings.TrimSpace(strings.ToLower(spoken))
	q = trimTrailingPunct(q)
	q = strings.TrimSpace(q)
	if q == "" {
		return "", false, false
	}

	// Rule 1: number / ordinal → senders[n-1].
	if n, hit := parseChannelNumber(q); hit {
		if n >= 1 && n <= len(senders) {
			return senders[n-1].ID, false, true
		}
	}

	// Rule 2: exact id / label match.
	for _, s := range senders {
		if strings.ToLower(s.ID) == q || strings.ToLower(s.Label) == q {
			return s.ID, false, true
		}
	}

	// Rule 3: substring match in id or label, first wins.
	for _, s := range senders {
		if strings.Contains(strings.ToLower(s.ID), q) || strings.Contains(strings.ToLower(s.Label), q) {
			return s.ID, false, true
		}
	}

	// Rule 4: cancel words.
	normalized := strings.Join(strings.Fields(q), "")
	if cancelWords[q] || cancelWords[normalized] {
		return "", true, false
	}

	// Rule 5: no match.
	return "", false, false
}
