package opcode

import "testing"

// Expected values with their line in ~/tortoise-wow/src/game/Protocol/Opcodes_1_12_1.h.
func TestKnownOpcodes(t *testing.T) {
	cases := []struct {
		got, want uint16
		line      int
	}{
		{CMSGCharEnum, 0x037, 58}, {SMSGCharEnum, 0x03B, 62}, {CMSGPlayerLogin, 0x03D, 64},
		{CMSGLogoutRequest, 0x04B, 78}, {SMSGLogoutComplete, 0x04D, 80}, {CMSGNameQuery, 0x050, 83},
		{CMSGGroupInvite, 0x06E, 113}, {SMSGGroupList, 0x07D, 128}, {CMSGMessagechat, 0x095, 152},
		{SMSGMessagechat, 0x096, 153}, {SMSGUpdateObject, 0x0A9, 172}, {MSGMoveHeartbeat, 0x0EE, 241},
		{CMSGCastSpell, 0x12E, 305}, {CMSGAttackswing, 0x141, 324}, {SMSGAttackerstateupdate, 0x14A, 333},
		{CMSGPing, 0x1DC, 479}, {SMSGAuthChallenge, 0x1EC, 495}, {CMSGAuthSession, 0x1ED, 496},
		{SMSGAuthResponse, 0x1EE, 497}, {SMSGCompressedUpdateObject, 0x1F6, 505},
		{SMSGLoginVerifyWorld, 0x236, 569}, {SMSGPeriodicauralog, 0x24E, 593},
		{SMSGSpellnonmeleedamagelog, 0x250, 595}, {SMSGWardenData, 0x2E6, 745},
		{SMSGAddonInfo, 0x2EF, 754}, {SMSGCompressedMoves, 0x2FB, 766},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("Opcodes_1_12_1.h:%d: got 0x%03X want 0x%03X", c.line, c.got, c.want)
		}
	}
	if NumMsgTypes != 0x33C {
		t.Errorf("NumMsgTypes = 0x%03X, want 0x33C", NumMsgTypes)
	}
	if Name(0x095) != "CMSG_MESSAGECHAT" || Name(0x329) != "UNKNOWN_0x329" {
		t.Errorf("names: %s %s", Name(0x095), Name(0x329))
	}
}
