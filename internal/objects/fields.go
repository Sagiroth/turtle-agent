package objects

// Update-field indices (Objects/UpdateFields.h; OBJECT_END = 0x06).
const (
	FieldGUID        uint16 = 0x00 // OBJECT_FIELD_GUID, 2 words (:29)
	FieldType        uint16 = 0x02 // (:30)
	FieldEntry       uint16 = 0x03 // (:31)
	UnitCharmedBy    uint16 = 0x0A // UNIT_FIELD_CHARMEDBY, 2 words (:74)
	UnitSummonedBy   uint16 = 0x0C // UNIT_FIELD_SUMMONEDBY, 2 words (:75)
	UnitCreatedBy    uint16 = 0x0E // UNIT_FIELD_CREATEDBY, 2 words (:76); hunter pets and totems (Pet.cpp:308, Totem.cpp:162)
	UnitTarget       uint16 = 0x10 // UNIT_FIELD_TARGET, 2 words (:77)
	UnitHealth       uint16 = 0x16 // (:80)
	UnitPower1       uint16 = 0x17 // POWER1..5 (:81-85)
	UnitMaxHealth    uint16 = 0x1C // (:86)
	UnitMaxPower1    uint16 = 0x1D // MAXPOWER1..5 (:87-91)
	UnitLevel        uint16 = 0x22 // (:92)
	UnitFaction      uint16 = 0x23 // UNIT_FIELD_FACTIONTEMPLATE (:93)
	UnitBytes0       uint16 = 0x24 // race, class, gender, power type (:94; Unit.h:455)
	UnitFlags        uint16 = 0x2E // (:97; FIELD_UNIT_FIELD_FLAGS = 46 at :345)
	UnitAura         uint16 = 0x2F // 48 slots of spell ids (:98-99)
	UnitDynamicFlags uint16 = 0x8F // (:128; FIELD_UNIT_DYNAMIC_FLAGS = 143 at :346)
	UnitNPCFlags     uint16 = 0x93 // (:132; FIELD_UNIT_NPC_FLAGS = 147 at :347)
)

// Aura slots (Spells/SpellAuraDefines.h:25-26).
const (
	AuraSlots         = 48
	PositiveAuraSlots = 32
)

// Unit flags (Objects/UnitDefines.h:402-441).
const (
	UnitFlagSpawning         uint32 = 0x00000002
	UnitFlagPlayerControlled uint32 = 0x00000008
	UnitFlagImmuneToPlayer   uint32 = 0x00000100
	UnitFlagInCombat         uint32 = 0x00080000
	UnitFlagNotSelectable    uint32 = 0x02000000
)

// Type ids (ObjectGuid.h:35-42).
const (
	TypeItem       uint8 = 1
	TypeContainer  uint8 = 2
	TypeUnit       uint8 = 3
	TypePlayer     uint8 = 4
	TypeGameObject uint8 = 5
)

// Item and player inventory fields (Objects/UpdateFields.h).
const (
	ItemStackCount uint16 = 0x0E // ITEM_FIELD_STACK_COUNT, OBJECT_END + 0x08 (:43)
	// PlayerInvSlotHead is PLAYER_FIELD_INV_SLOT_HEAD (:202): the guid of inventory slot i,
	// two words each. The 23 equipment and bag slots run on into the 16 backpack slots,
	// PLAYER_FIELD_PACK_SLOT_1 (:204) = head + 46. The owner alone gets these fields.
	PlayerInvSlotHead uint16 = 0x1E6
)

// The backpack is bag INVENTORY_SLOT_BAG_0, slots 23-38 (Objects/Player.h:583, :618-619).
const (
	BackpackBag   uint8 = 255
	BackpackFirst uint8 = 23
	BackpackEnd   uint8 = 39
)
