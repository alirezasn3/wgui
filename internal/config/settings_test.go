package config

import (
	"strings"
	"testing"
)

func TestTelegramSettingsAreRepairedNotRejected(t *testing.T) {
	def := DefaultSettings().Telegram

	// Clearing a box and saving must not leave the bot with nothing to say.
	s := DefaultSettings()
	s.Telegram.Messages.Linked = "   "
	s.Telegram.Messages.ExpiryWarning = ""
	s.Telegram.Messages.UsageWarning = "  trimmed {name}  "
	s.Normalize()
	if s.Telegram.Messages.Linked != def.Messages.Linked || s.Telegram.Messages.ExpiryWarning != def.Messages.ExpiryWarning {
		t.Errorf("empty messages = %q / %q, want the defaults back", s.Telegram.Messages.Linked, s.Telegram.Messages.ExpiryWarning)
	}
	if s.Telegram.Messages.UsageWarning != "trimmed {name}" {
		t.Errorf("message = %q, want it trimmed", s.Telegram.Messages.UsageWarning)
	}

	// Cut by characters, never bytes: half a Persian letter is not text.
	s = DefaultSettings()
	s.Telegram.Messages.Help = strings.Repeat("س", maxMessageRunes+50)
	s.Normalize()
	if got := []rune(s.Telegram.Messages.Help); len(got) != maxMessageRunes || strings.ContainsRune(string(got), '�') {
		t.Errorf("a long message is %d characters (valid: %v), want %d", len(got), !strings.ContainsRune(string(got), '�'), maxMessageRunes)
	}

	s = DefaultSettings()
	s.Telegram.WarnDays, s.Telegram.WarnUsageBytes = -3, -1
	s.Normalize()
	if s.Telegram.WarnDays != 0 || s.Telegram.WarnUsageBytes != 0 {
		t.Errorf("negative thresholds = %d / %d, want them off", s.Telegram.WarnDays, s.Telegram.WarnUsageBytes)
	}
	s.Telegram.WarnDays = 100000
	s.Normalize()
	if s.Telegram.WarnDays != 365 {
		t.Errorf("warn days = %d, want it capped at 365", s.Telegram.WarnDays)
	}
}

// The thresholds and the wording are policy, so a node follows its master's —
// unlike the two settings that describe the box a server is.
func TestANodeFollowsTheMastersTelegramPolicy(t *testing.T) {
	master := DefaultSettings()
	master.Telegram.WarnDays = 7
	master.Telegram.Messages.UsageWarning = "از مرکز: {name}"
	node := DefaultSettings()
	node.PublicAddress = "node.example.com"

	adopted := node.AdoptShared(master)
	if adopted.Telegram.WarnDays != 7 || adopted.Telegram.Messages.UsageWarning != "از مرکز: {name}" {
		t.Errorf("telegram = %+v, want the master's", adopted.Telegram)
	}
	if adopted.PublicAddress != "node.example.com" {
		t.Errorf("public address = %q, want the node's own", adopted.PublicAddress)
	}
}
