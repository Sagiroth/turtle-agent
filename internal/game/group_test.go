package game

import (
	"bytes"
	"testing"

	"github.com/pjerra/turtle-agent/internal/packet"
)

// groupListBody transcribes Group::SendUpdate (Group.cpp:1402-1432) for the receiving player.
func groupListBody(leader packet.GUID, members ...GroupMember) []byte {
	w := packet.NewWriter().U8(0).U8(0).U32(uint32(len(members)))
	for _, m := range members {
		w.CString(m.Name).GUID(m.GUID).U8(m.Status).U8(m.Flags)
	}
	w.GUID(leader)
	if len(members) > 0 {
		w.U8(3).U64(0).U8(2).U8(0) // group loot, no looter, uncommon threshold, difficulty byte
	}
	return w.Bytes()
}

func TestGroupInviteAndResult(t *testing.T) {
	if !bytes.Equal(BuildGroupInvite("Gorm"), []byte("Gorm\x00")) {
		t.Fatal("invite body")
	}
	res, err := ParsePartyCommandResult(packet.NewWriter().U32(0).CString("Gorm").U32(4).Bytes())
	if err != nil || res.Result != 4 || res.Text() != "Gorm is already in a group." {
		t.Fatalf("%+v %q %v", res, res.Text(), err)
	}
}

func TestGroupListTwoMembers(t *testing.T) {
	body := groupListBody(0x2A, GroupMember{"Gorm", 0x30, MemberOnline, 0}, GroupMember{"Lia", 0x31, MemberOnline | MemberDead, 0})
	g, err := ParseGroupList(body)
	if err != nil || len(g.Members) != 2 || g.Members[1].Name != "Lia" || g.Leader != 0x2A || g.LootThreshold != 2 || g.Extra != 1 {
		t.Fatalf("%+v %v", g, err)
	}
}

// Review Focus 3: bytes past the fields we read (difficulty, or the 24-byte empty list) are tolerated.
func TestGroupListTrailingBytes(t *testing.T) {
	g, err := ParseGroupList(make([]byte, 24)) // Group.cpp:493-494
	if err != nil || len(g.Members) != 0 || g.Extra != 10 {
		t.Fatalf("%+v %v", g, err)
	}
	g, err = ParseGroupList(append(groupListBody(0x2A, GroupMember{"Gorm", 0x30, 1, 0}), 0xEE, 0xEE))
	if err != nil || g.Extra != 3 {
		t.Fatalf("%+v %v", g, err)
	}
	if _, err := ParseGroupList([]byte{0, 0, 5, 0, 0, 0}); err == nil {
		t.Fatal("truncated list accepted")
	}
}

// memberStatsBody transcribes BuildPartyMemberStatsPacket (GroupHandler.cpp:657-801), no pet.
func memberStatsBody(g packet.GUID, mask uint32) []byte {
	w := packet.NewWriter().PackedGUID(g).U32(mask)
	if mask&GroupUpdateStatus != 0 {
		w.U8(MemberOnline)
	}
	if mask&GroupUpdateCurHP != 0 {
		w.U16(500)
	}
	if mask&GroupUpdateMaxHP != 0 {
		w.U16(600)
	}
	if mask&GroupUpdatePowerType != 0 {
		w.U8(0)
	}
	if mask&GroupUpdateCurPower != 0 {
		w.U16(200)
	}
	if mask&GroupUpdateMaxPower != 0 {
		w.U16(300)
	}
	if mask&GroupUpdateLevel != 0 {
		w.U16(60)
	}
	if mask&GroupUpdateZone != 0 {
		w.U16(12)
	}
	if mask&GroupUpdatePosition != 0 {
		w.U16(100).U16(200)
	}
	if mask&GroupUpdateAuras != 0 {
		w.U32(0b101).U16(1243).U16(1459) // slots 0 and 2
	}
	if mask&GroupUpdateAurasNegative != 0 {
		w.U16(0b1).U16(589) // slot 32
	}
	if mask&GroupUpdatePetGUID != 0 {
		w.U64(0)
	}
	if mask&GroupUpdatePetName != 0 {
		w.U8(0)
	}
	for _, f := range []uint32{GroupUpdatePetModelID, GroupUpdatePetCurHP, GroupUpdatePetMaxHP} {
		if mask&f != 0 {
			w.U16(0)
		}
	}
	if mask&GroupUpdatePetPowerType != 0 {
		w.U8(0)
	}
	for _, f := range []uint32{GroupUpdatePetCurPower, GroupUpdatePetMaxPower} {
		if mask&f != 0 {
			w.U16(0)
		}
	}
	if mask&GroupUpdatePetAuras != 0 {
		w.U32(0)
	}
	if mask&GroupUpdatePetAurasNegative != 0 {
		w.U16(0)
	}
	return w.Bytes()
}

func TestPartyMemberStatsFullAndPartial(t *testing.T) {
	m, err := ParsePartyMemberStats(memberStatsBody(0x30, GroupUpdateFull))
	if err != nil || m.GUID != 0x30 || m.Status != MemberOnline || m.HP != 500 || m.MaxHP != 600 || m.Power != 200 ||
		m.Level != 60 || len(m.Auras) != 3 || m.Auras[2] != 589 {
		t.Fatalf("%+v %v", m, err)
	}
	m, err = ParsePartyMemberStats(memberStatsBody(0x30, GroupUpdateCurHP|GroupUpdateLevel))
	if err != nil || m.HP != 500 || m.Level != 60 || m.MaxHP != 0 {
		t.Fatalf("%+v %v", m, err)
	}
	offline := packet.NewWriter().PackedGUID(0x30).U32(GroupUpdateStatus).U8(0).Bytes() // GroupHandler.cpp:831-837
	if m, err := ParsePartyMemberStats(offline); err != nil || m.Status != 0 {
		t.Fatalf("%+v %v", m, err)
	}
}

// A present pet keeps the pet fields aligned (GroupHandler.cpp:722-791); truncation is an error.
func TestPartyMemberStatsPetAndTruncation(t *testing.T) {
	mask := GroupUpdatePetGUID | GroupUpdatePetName | GroupUpdatePetModelID | GroupUpdatePetCurHP | GroupUpdatePetMaxHP | GroupUpdateLevel
	body := packet.NewWriter().PackedGUID(0x30).U32(mask).
		U16(60).U64(0xF14000001).CString("Rex").U16(7).U16(90).U16(100).Bytes()
	m, err := ParsePartyMemberStats(body)
	if err != nil || m.PetName != "Rex" || m.PetHP != 90 || m.PetMaxHP != 100 || m.Level != 60 || m.PetGUID != 0xF14000001 {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err := ParsePartyMemberStats(body[:len(body)-1]); err == nil {
		t.Fatal("truncated stats accepted")
	}
}
