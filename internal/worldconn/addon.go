package worldconn

import (
	"bytes"
	"compress/zlib"

	"github.com/pjerra/turtle-agent/internal/packet"
)

// fingerprintAddons are the addons the anticheat reads its fingerprint from
// (Anticheat/AddonHandler.cpp:80-86). Flags 0x01 on all four means "no fingerprint
// yet": the server generates one and answers with SMSG_ADDON_INFO (:217-253).
var fingerprintAddons = []string{"Blizzard_BindingUI", "Blizzard_InspectUI", "Blizzard_MacroUI", "Blizzard_RaidUI"}

// AddonBlock is the tail of CMSG_AUTH_SESSION: u32 uncompressed size, then a zlib
// stream of name, u8 flags, u32 modulus CRC, u32 URL CRC (AddonHandler.cpp:114-139, :200).
func AddonBlock() []byte {
	plain := packet.NewWriter()
	for _, name := range fingerprintAddons {
		plain.CString(name).U8(0x01).U32(0x4C1C776D).U32(0) // standard modulus CRC (AddonHandler.cpp:141)
	}
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	zw.Write(plain.Bytes())
	zw.Close()
	return packet.NewWriter().U32(uint32(plain.Len())).Append(z.Bytes()).Bytes()
}
