// Chat, addon, channel and gossip packets written the way the core does. Line numbers
// cite the core at 187af78, the checkout deployed on the test server.
package gametest

import (
	"github.com/pjerra/turtle-agent/internal/game"
	"github.com/pjerra/turtle-agent/internal/packet"
)

// chatTagGM is CHAT_TAG_GM (Chat.h:90).
const chatTagGM = 3

// ChatPacket is ChatHandler::BuildChatPacket (Chat.cpp:2254-2314). name is written only for
// monster and boss types, target only where the type carries one, channel only for
// CHAT_MSG_CHANNEL (its player rank is written as 0). An empty final message ends the
// packet after the sender, as the core does.
func ChatPacket(t game.ChatType, lang uint32, sender packet.GUID, name string, target packet.GUID, channel, msg string, tag uint8) []byte {
	if tag == chatTagGM && lang != game.LangAddon { // Chat.cpp:2260-2263
		msg = "|c1049e6ff" + msg + "|r"
	}
	w := packet.NewWriter().U8(uint8(t)).U32(lang)
	switch t {
	case game.ChatMonsterWhisper, game.ChatRaidBossWhisper, game.ChatRaidBossEmote, game.ChatMonsterEmote:
		w.U32(uint32(len(name) + 1)).CString(name).GUID(target)
	case game.ChatSay, game.ChatParty, game.ChatYell:
		w.GUID(sender).GUID(sender)
	case game.ChatMonsterSay, game.ChatMonsterYell:
		w.GUID(sender).U32(uint32(len(name) + 1)).CString(name).GUID(target)
	case game.ChatChannel:
		w.CString(channel).U32(0).GUID(sender)
	default:
		w.GUID(sender)
	}
	if msg == "" { // Chat.cpp:2311-2312
		return w.Bytes()
	}
	return w.U32(uint32(len(msg) + 1)).CString(msg).U8(tag).Bytes()
}

// AddonMessage is Player::SendAddonMessage (Player.cpp:25603-25611): CHAT_MSG_GUILD in
// LANG_ADDON from the player himself, the way the TortoiseBots module answers its addon.
func AddonMessage(self packet.GUID, prefix, text string, tag uint8) []byte {
	return ChatPacket(game.ChatGuild, game.LangAddon, self, "", 0, "", prefix+"\t"+text, tag)
}

// PartyAddonMessage is a group member's addon message as CHAT_MSG_PARTY relays it to the
// group (ChatHandler.cpp:664-689).
func PartyAddonMessage(sender packet.GUID, prefix, text string) []byte {
	return ChatPacket(game.ChatParty, game.LangAddon, sender, "", 0, "", prefix+"\t"+text, 0)
}
